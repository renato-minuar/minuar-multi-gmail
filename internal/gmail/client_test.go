package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"google.golang.org/api/option"
)

type fakeAPI struct {
	srv          *httptest.Server
	labelCalls   atomic.Int32
	labelFailN   int32 // first N label calls answer 503
	profileCalls atomic.Int32
	profileFailN int32 // first N profile calls answer 503
	modifyBody   string
	draftBody    string
	deleted      []string
	threadCalls  atomic.Int32
	routeDelay   time.Duration // sleep before answering the message, attachment and upload routes

	uploadMeta     string // JSON metadata part of the last media upload
	uploadMedia    []byte // media part of the last media upload
	uploadType     string // Content-Type of the media part
	uploadQuery    string // uploadType query parameter
	uploadCalls    atomic.Int32
	uploadFailN    int32 // first N upload calls answer 503
	uploadFailCode int   // if non-zero, every upload answers this code
}

func apiError(w http.ResponseWriter, code int, reason, msg string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": code, "message": msg, "errors": []map[string]string{{"reason": reason, "message": msg}},
	}})
}

func fixtureJSON(t *testing.T, name string) string {
	m := loadFixture(t, name)
	b, _ := json.Marshal(m)
	return string(b)
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	plain := fixtureJSON(t, "plain.json")
	htmlOnly := fixtureJSON(t, "html_only.json")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/profile", func(w http.ResponseWriter, r *http.Request) {
		n := f.profileCalls.Add(1)
		if n <= f.profileFailN {
			apiError(w, 503, "backendError", "Backend Error")
			return
		}
		io.WriteString(w, `{"emailAddress":"control@example.com"}`)
	})
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		n := f.labelCalls.Add(1)
		if n <= f.labelFailN {
			apiError(w, 503, "backendError", "Backend Error")
			return
		}
		io.WriteString(w, `{"labels":[{"id":"INBOX","name":"INBOX","type":"system"},{"id":"UNREAD","name":"UNREAD","type":"system"},{"id":"Label_7","name":"Clients/Acme","type":"user"}]}`)
	})
	mux.HandleFunc("GET /gmail/v1/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") != "from:ann" || q.Get("maxResults") != "2" || q.Get("pageToken") != "pt" || q.Get("includeSpamTrash") != "true" {
			apiError(w, 400, "badRequest", "unexpected query "+r.URL.RawQuery)
			return
		}
		io.WriteString(w, `{"threads":[{"id":"t-1","snippet":"s1"},{"id":"t-2","snippet":"s2"}],"nextPageToken":"np"}`)
	})
	mux.HandleFunc("GET /gmail/v1/users/me/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.threadCalls.Add(1)
		switch r.PathValue("id") {
		case "t-1":
			io.WriteString(w, `{"id":"t-1","snippet":"thread one","messages":[`+plain+`,`+htmlOnly+`]}`)
		case "t-2":
			io.WriteString(w, `{"id":"t-2","snippet":"thread two","messages":[`+htmlOnly+`]}`)
		default:
			apiError(w, 404, "notFound", "Requested entity was not found.")
		}
	})
	mux.HandleFunc("POST /gmail/v1/users/me/threads/{id}/modify", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.modifyBody = string(b)
		io.WriteString(w, `{"id":"t-1","messages":[{"id":"m-plain","labelIds":["INBOX","Label_7"]},{"id":"m-html","labelIds":["Label_7"]}]}`)
	})
	mux.HandleFunc("POST /gmail/v1/users/me/threads/{id}/trash", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"t-1","messages":[{"id":"m-plain","labelIds":["TRASH"]}]}`)
	})
	mux.HandleFunc("POST /gmail/v1/users/me/threads/{id}/untrash", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"t-1","messages":[{"id":"m-plain","labelIds":["INBOX"]}]}`)
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(f.routeDelay)
		if r.PathValue("id") != "m-plain" {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		if r.URL.Query().Get("format") == "raw" {
			// base64url of "From: a@x.com\r\n\r\nhi", unpadded.
			io.WriteString(w, `{"id":"m-plain","threadId":"t-1","raw":"RnJvbTogYUB4LmNvbQ0KDQpoaQ"}`)
			return
		}
		io.WriteString(w, plain)
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}/attachments/{aid}", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(f.routeDelay)
		if r.PathValue("id") != "m-plain" || r.PathValue("aid") != "att-1" {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		// base64url of "hello", unpadded.
		io.WriteString(w, `{"attachmentId":"att-1","size":5,"data":"aGVsbG8"}`)
	})
	mux.HandleFunc("POST /upload/gmail/v1/users/me/drafts", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(f.routeDelay)
		n := f.uploadCalls.Add(1)
		if n <= f.uploadFailN {
			apiError(w, 503, "backendError", "Backend Error")
			return
		}
		if f.uploadFailCode != 0 {
			apiError(w, f.uploadFailCode, "uploadTooLarge", "Request Entity Too Large")
			return
		}
		f.uploadQuery = r.URL.Query().Get("uploadType")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || params["boundary"] == "" {
			apiError(w, 400, "badRequest", "not multipart: "+r.Header.Get("Content-Type"))
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		meta, err := mr.NextPart()
		if err != nil {
			apiError(w, 400, "badRequest", "no metadata part")
			return
		}
		mb, _ := io.ReadAll(meta)
		f.uploadMeta = string(mb)
		media, err := mr.NextPart()
		if err != nil {
			apiError(w, 400, "badRequest", "no media part")
			return
		}
		f.uploadType = media.Header.Get("Content-Type")
		f.uploadMedia, _ = io.ReadAll(media)
		io.WriteString(w, `{"id":"d-2","message":{"id":"m-10","threadId":"t-1"}}`)
	})
	mux.HandleFunc("POST /gmail/v1/users/me/drafts", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.draftBody = string(b)
		io.WriteString(w, `{"id":"d-1","message":{"id":"m-9","threadId":"t-1"}}`)
	})
	mux.HandleFunc("GET /gmail/v1/users/me/drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "d-1" {
			apiError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		io.WriteString(w, `{"id":"d-1","message":{"id":"m-9","threadId":"t-1","payload":{"headers":[{"name":"To","value":"bob@example.com"},{"name":"Cc","value":"carol@example.com"},{"name":"Subject","value":"Draft subject"}]}}}`)
	})
	mux.HandleFunc("POST /gmail/v1/users/me/drafts/send", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"m-9","threadId":"t-1","labelIds":["SENT"]}`)
	})
	mux.HandleFunc("DELETE /gmail/v1/users/me/drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.deleted = append(f.deleted, r.PathValue("id"))
		w.WriteHeader(204)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func newTestClient(t *testing.T, f *fakeAPI) *Client {
	c, err := New(context.Background(), "work", "control@example.com",
		option.WithEndpoint(f.srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(time.Duration) {}
	return c
}

func TestProfileEmail(t *testing.T) {
	f := newFakeAPI(t)
	email, err := ProfileEmail(context.Background(), option.WithEndpoint(f.srv.URL+"/"), option.WithoutAuthentication())
	if err != nil || email != "control@example.com" {
		t.Fatalf("email = %q err %v", email, err)
	}
}

// withNoSleep stubs the package-level retry pause so ProfileEmail's tests
// don't wait out the real 1s-plus-jitter backoff, and restores it after.
func withNoSleep(t *testing.T) {
	t.Helper()
	orig := sleepDefault
	sleepDefault = func(time.Duration) {}
	t.Cleanup(func() { sleepDefault = orig })
}

func TestProfileEmailRetriesOnceOn503(t *testing.T) {
	withNoSleep(t)
	f := newFakeAPI(t)
	f.profileFailN = 1
	email, err := ProfileEmail(context.Background(), option.WithEndpoint(f.srv.URL+"/"), option.WithoutAuthentication())
	if err != nil || email != "control@example.com" {
		t.Fatalf("email = %q err %v", email, err)
	}
	if f.profileCalls.Load() != 2 {
		t.Fatalf("calls = %d, want exactly 2", f.profileCalls.Load())
	}
}

func TestProfileEmailFailsAfterOneRetry(t *testing.T) {
	withNoSleep(t)
	f := newFakeAPI(t)
	f.profileFailN = 2
	_, err := ProfileEmail(context.Background(), option.WithEndpoint(f.srv.URL+"/"), option.WithoutAuthentication())
	if err == nil || !strings.Contains(err.Error(), "Backend Error") {
		t.Fatalf("err = %v", err)
	}
	if f.profileCalls.Load() != 2 {
		t.Fatalf("calls = %d, want exactly 2", f.profileCalls.Load())
	}
}

func TestSearchThreads(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	res, err := c.SearchThreads(context.Background(), SearchParams{Query: "from:ann", MaxResults: 2, PageToken: "pt", IncludeSpamTrash: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.NextPageToken != "np" || len(res.Threads) != 2 {
		t.Fatalf("res = %+v", res)
	}
	one := res.Threads[0]
	if one.ThreadID != "t-1" || one.MessageCount != 2 || one.Subject != "HTML subject" || one.From != "ann@example.com" || !one.Unread {
		t.Fatalf("thread one = %+v", one)
	}
	if strings.Join(one.Labels, ",") != "INBOX,UNREAD" || one.Snippet != "thread one" {
		t.Fatalf("thread one = %+v", one)
	}
	if res.Threads[1].ThreadID != "t-2" {
		t.Fatal("order must follow the list response")
	}
	if c.Email() != "control@example.com" {
		t.Fatal(c.Email())
	}
}

func TestGetThreadAndMessage(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	th, err := c.GetThread(context.Background(), "t-1", 5000)
	if err != nil || th.ThreadID != "t-1" || len(th.Messages) != 2 {
		t.Fatalf("thread = %+v err %v", th, err)
	}
	if th.Messages[0].BodyText != "Hello from plain text.\n" || th.Messages[1].BodySource != "text/html" {
		t.Fatalf("messages = %+v", th.Messages)
	}
	if strings.Join(th.Messages[0].Labels, ",") != "INBOX,UNREAD" {
		t.Fatalf("labels = %v", th.Messages[0].Labels)
	}
	m, err := c.GetMessage(context.Background(), "m-plain", 5)
	if err != nil || m.BodyText != "Hello" || !m.BodyTruncated {
		t.Fatalf("message = %+v err %v", m, err)
	}
	_, err = c.GetThread(context.Background(), "t-missing", 100)
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.ID != "t-missing" {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}

func TestGetMessageHeaders(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	h, err := c.GetMessageHeaders(context.Background(), "m-plain")
	if err != nil {
		t.Fatal(err)
	}
	if h.MessageID != "m-plain" || h.ThreadID != "t-1" || h.RFCMessageID != "<plain@mail.example.com>" ||
		h.From != "Ann Example <ann@example.com>" || h.ReplyTo != "list@example.com" || h.Subject != "Plain subject" {
		t.Fatalf("headers = %+v", h)
	}
	// sizeEstimate comes back from the metadata call and lets the eml
	// forward check the size cap before downloading the raw message.
	if h.SizeEstimate != 4321 {
		t.Fatalf("size estimate = %d, want 4321", h.SizeEstimate)
	}
	// splitAddresses renders through net/mail's Address.String() (Task 6 fix
	// 47f366e, for RFC 5322 correctness), which always brackets the addr-spec
	// and quotes a display name; hence the canonical form here, not the raw
	// header text.
	if strings.Join(h.To, "|") != `<control@example.com>|"Bob" <bob@example.com>` || strings.Join(h.Cc, "|") != "<carol@example.com>" {
		t.Fatalf("to=%v cc=%v", h.To, h.Cc)
	}
}

func TestListLabelsSortedByName(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	labels, err := c.ListLabels(context.Background())
	if err != nil || len(labels) != 3 {
		t.Fatalf("labels = %+v err %v", labels, err)
	}
	if labels[0].Name != "Clients/Acme" || labels[0].ID != "Label_7" || labels[0].Type != "user" {
		t.Fatalf("labels = %+v", labels)
	}
}

func TestModifyThreadLabelsResolvesNamesAndIDs(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	after, err := c.ModifyThreadLabels(context.Background(), "t-1", []string{"Clients/Acme", "STARRED"}, []string{"UNREAD", "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.modifyBody, `"addLabelIds":["Label_7","STARRED"]`) || !strings.Contains(f.modifyBody, `"removeLabelIds":["UNREAD","INBOX"]`) {
		t.Fatalf("modify body = %s", f.modifyBody)
	}
	if strings.Join(after, ",") != "INBOX,Clients/Acme" {
		t.Fatalf("after = %v", after)
	}
}

func TestModifyThreadLabelsUnknownNameListsLabels(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	_, err := c.ModifyThreadLabels(context.Background(), "t-1", []string{"Nope"}, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown label "Nope"`) || !strings.Contains(err.Error(), "Clients/Acme") {
		t.Fatalf("err = %v", err)
	}
	if f.labelCalls.Load() != 2 {
		t.Fatalf("label list must be refreshed once on a miss; calls = %d", f.labelCalls.Load())
	}
	if _, err := c.ModifyThreadLabels(context.Background(), "t-1", nil, nil); err == nil {
		t.Fatal("empty add and remove must error")
	}
}

// TestModifyThreadLabelsSystemOnlySkipsCacheLoad guards against
// resolveLabelIDs loading the label cache before checking whether a value
// is a system ID: a call made only of system IDs (e.g. archive = remove
// INBOX, mark read = remove UNREAD) must never call labels.list.
func TestModifyThreadLabelsSystemOnlySkipsCacheLoad(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	after, err := c.ModifyThreadLabels(context.Background(), "t-1", []string{"STARRED"}, []string{"INBOX", "UNREAD"})
	if err != nil {
		t.Fatal(err)
	}
	if f.labelCalls.Load() != 0 {
		t.Fatalf("system-only labels must not trigger labels.list; calls = %d", f.labelCalls.Load())
	}
	if !strings.Contains(f.modifyBody, `"addLabelIds":["STARRED"]`) || !strings.Contains(f.modifyBody, `"removeLabelIds":["INBOX","UNREAD"]`) {
		t.Fatalf("modify body = %s", f.modifyBody)
	}
	if after == nil {
		t.Fatal("after must not be nil")
	}
}

func TestDraftsAndSend(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	d, err := c.CreateDraft(context.Background(), []byte("From: a@x.com\r\n\r\nhi"), "t-1")
	if err != nil || d.DraftID != "d-1" || d.MessageID != "m-9" || d.ThreadID != "t-1" {
		t.Fatalf("draft = %+v err %v", d, err)
	}
	if !strings.Contains(f.draftBody, `"threadId":"t-1"`) || !strings.Contains(f.draftBody, `"raw":"RnJvbTogYUB4LmNvbQ0KDQpoaQ"`) {
		t.Fatalf("draft body = %s", f.draftBody)
	}
	s, err := c.SendDraft(context.Background(), "d-1")
	if err != nil || s.MessageID != "m-9" || s.ThreadID != "t-1" || s.Subject != "Draft subject" {
		t.Fatalf("send = %+v err %v", s, err)
	}
	// See the note in TestGetMessageHeaders: splitAddresses always brackets
	// the addr-spec via net/mail.Address.String().
	if strings.Join(s.To, ",") != "<bob@example.com>" || strings.Join(s.Cc, ",") != "<carol@example.com>" {
		t.Fatalf("send = %+v", s)
	}
	_, err = c.SendDraft(context.Background(), "d-missing")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError before any send", err)
	}
	if err := c.DeleteDraft(context.Background(), "d-1"); err != nil || strings.Join(f.deleted, ",") != "d-1" {
		t.Fatalf("delete err %v deleted %v", err, f.deleted)
	}
}

func TestTrashUntrash(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	after, err := c.TrashThread(context.Background(), "t-1")
	if err != nil || strings.Join(after, ",") != "TRASH" {
		t.Fatalf("after = %v err %v", after, err)
	}
	after, err = c.UntrashThread(context.Background(), "t-1")
	if err != nil || strings.Join(after, ",") != "INBOX" {
		t.Fatalf("after = %v err %v", after, err)
	}
}

func TestRetryOn503Once(t *testing.T) {
	f := newFakeAPI(t)
	f.labelFailN = 1
	c := newTestClient(t, f)
	var slept []time.Duration
	c.sleep = func(d time.Duration) { slept = append(slept, d) }
	if _, err := c.ListLabels(context.Background()); err != nil {
		t.Fatalf("expected success after one retry: %v", err)
	}
	if f.labelCalls.Load() != 2 || len(slept) != 1 || slept[0] < time.Second || slept[0] > 1500*time.Millisecond {
		t.Fatalf("calls = %d slept = %v", f.labelCalls.Load(), slept)
	}
}

func TestNoSecondRetry(t *testing.T) {
	f := newFakeAPI(t)
	f.labelFailN = 2
	c := newTestClient(t, f)
	if _, err := c.ListLabels(context.Background()); err == nil || !strings.Contains(err.Error(), "Backend Error") {
		t.Fatalf("err = %v", err)
	}
	if f.labelCalls.Load() != 2 {
		t.Fatalf("calls = %d, want exactly 2", f.labelCalls.Load())
	}
}

func TestNoRetryOn4xxAndReauthMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/labels"):
			apiError(w, 401, "authError", "Invalid Credentials")
		case strings.HasSuffix(r.URL.Path, "/profile"):
			apiError(w, 403, "insufficientPermissions", "Insufficient Permission")
		default:
			apiError(w, 400, "badRequest", "bad")
		}
	}))
	defer srv.Close()
	c, _ := New(context.Background(), "work", "control@example.com", option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	c.sleep = func(time.Duration) { t.Fatal("must not sleep on 4xx") }
	_, err := c.ListLabels(context.Background())
	var re *googleauth.ReauthError
	if !errors.As(err, &re) || re.Alias != "work" {
		t.Fatalf("401 err = %v, want ReauthError", err)
	}
	_, err = c.GetMessageHeaders(context.Background(), "m-1")
	if err == nil || errors.As(err, &re) {
		t.Fatalf("400 must pass through unchanged: %v", err)
	}
	_, err = ProfileEmail(context.Background(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err == nil {
		t.Fatal("403 must be an error")
	}
}

// TestClientReauthMappingOn403 exercises Client.mapErr's own 403
// insufficientPermissions branch. TestNoRetryOn4xxAndReauthMapping's 403
// case goes through the package-level ProfileEmail, which has no
// alias/email and never calls mapErr, so it only proves 403 is an error,
// not that a Client maps it to ReauthError.
func TestClientReauthMappingOn403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiError(w, 403, "insufficientPermissions", "Insufficient Permission")
	}))
	defer srv.Close()
	c, err := New(context.Background(), "work", "control@example.com", option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(time.Duration) { t.Fatal("must not sleep on 4xx") }
	_, err = c.ListLabels(context.Background())
	var re *googleauth.ReauthError
	if !errors.As(err, &re) || re.Alias != "work" || re.Email != "control@example.com" {
		t.Fatalf("403 err = %v, want ReauthError", err)
	}
}

func TestGetAttachment(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	data, err := c.GetAttachment(context.Background(), "m-plain", "att-1")
	if err != nil || string(data) != "hello" {
		t.Fatalf("data = %q err %v", data, err)
	}
	_, err = c.GetAttachment(context.Background(), "m-plain", "att-missing")
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.ID != "att-missing" {
		t.Fatalf("err = %v, want NotFoundError for att-missing", err)
	}
}

func TestGetRawMessage(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	raw, err := c.GetRawMessage(context.Background(), "m-plain")
	if err != nil || string(raw) != "From: a@x.com\r\n\r\nhi" {
		t.Fatalf("raw = %q err %v", raw, err)
	}
	_, err = c.GetRawMessage(context.Background(), "m-gone")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}

// TestTransferCallsUseTheLongerTimeout proves the three transfer calls
// (attachment download, raw fetch, draft upload) run under TransferTimeout
// while every other call keeps the shorter CallTimeout. Both timeouts are
// lowered so the test costs milliseconds, and the same delayed routes serve
// both kinds of call: only the bound differs.
func TestTransferCallsUseTheLongerTimeout(t *testing.T) {
	origCall, origTransfer := CallTimeout, TransferTimeout
	CallTimeout, TransferTimeout = 50*time.Millisecond, 2*time.Second
	t.Cleanup(func() { CallTimeout, TransferTimeout = origCall, origTransfer })
	f := newFakeAPI(t)
	f.routeDelay = 300 * time.Millisecond
	c := newTestClient(t, f)
	data, err := c.GetAttachment(context.Background(), "m-plain", "att-1")
	if err != nil || string(data) != "hello" {
		t.Fatalf("attachment download = %q err %v, want it to outlast the call timeout", data, err)
	}
	raw, err := c.GetRawMessage(context.Background(), "m-plain")
	if err != nil || string(raw) != "From: a@x.com\r\n\r\nhi" {
		t.Fatalf("raw fetch = %q err %v, want it to outlast the call timeout", raw, err)
	}
	if _, err := c.CreateDraftUpload(context.Background(), []byte("From: a@x.com\r\n\r\nhi"), "t-1"); err != nil {
		t.Fatalf("draft upload err %v, want it to outlast the call timeout", err)
	}
	if _, err := c.GetMessageHeaders(context.Background(), "m-plain"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("headers err = %v, want a deadline error on the same delayed route", err)
	}
}

func TestCreateDraftUpload(t *testing.T) {
	f := newFakeAPI(t)
	c := newTestClient(t, f)
	raw := []byte("From: a@x.com\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n--b\r\n\r\nhi\r\n--b--\r\n")
	d, err := c.CreateDraftUpload(context.Background(), raw, "t-1")
	if err != nil || d.DraftID != "d-2" || d.MessageID != "m-10" || d.ThreadID != "t-1" {
		t.Fatalf("draft = %+v err %v", d, err)
	}
	if f.uploadQuery != "multipart" {
		t.Fatalf("uploadType = %q", f.uploadQuery)
	}
	if !strings.Contains(f.uploadMeta, `"threadId":"t-1"`) || strings.Contains(f.uploadMeta, `"raw"`) {
		t.Fatalf("metadata = %s (thread id required, raw must be absent)", f.uploadMeta)
	}
	if f.uploadType != "message/rfc822" {
		t.Fatalf("media type = %q", f.uploadType)
	}
	if string(f.uploadMedia) != string(raw) {
		t.Fatalf("media = %q, want the raw message", f.uploadMedia)
	}
}

func TestCreateDraftUploadRetriesOn503(t *testing.T) {
	f := newFakeAPI(t)
	f.uploadFailN = 1
	c := newTestClient(t, f)
	d, err := c.CreateDraftUpload(context.Background(), []byte("From: a@x.com\r\n\r\nhi"), "")
	if err != nil || d.DraftID != "d-2" {
		t.Fatalf("draft = %+v err %v", d, err)
	}
	if f.uploadCalls.Load() != 2 {
		t.Fatalf("upload calls = %d, want 2", f.uploadCalls.Load())
	}
}

func TestCreateDraftUploadTooLarge(t *testing.T) {
	f := newFakeAPI(t)
	f.uploadFailCode = 413
	c := newTestClient(t, f)
	raw := []byte("From: a@x.com\r\n\r\nhi")
	_, err := c.CreateDraftUpload(context.Background(), raw, "")
	var tl *TooLargeError
	if !errors.As(err, &tl) || tl.Bytes != len(raw) {
		t.Fatalf("err = %v, want TooLargeError with %d bytes", err, len(raw))
	}
	if !strings.Contains(err.Error(), "message too large for Gmail (") {
		t.Fatalf("err text = %q", err.Error())
	}
	if f.uploadCalls.Load() != 1 {
		t.Fatalf("413 must not be retried, calls = %d", f.uploadCalls.Load())
	}
}
