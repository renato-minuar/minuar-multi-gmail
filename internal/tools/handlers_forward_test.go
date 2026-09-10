package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	stdmime "mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// forwardFixture wires a work account with one original message that has
// two attachments and returns the env.
func forwardFixture(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.work.headers = &gmail.MessageHeaders{
		MessageID: "m-1", ThreadID: "t-orig", RFCMessageID: "<orig@mail.example.com>", References: []string{"<root@mail.example.com>"},
		Subject: "Plain subject", From: "Ann <ann@example.com>", To: []string{"control@example.com"}, Cc: nil,
	}
	e.work.message = &gmail.Message{
		MessageID: "m-1", ThreadID: "t-orig", Date: time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC),
		From: "Ann <ann@example.com>", To: []string{"control@example.com", "bob@example.com"}, Cc: []string{"carol@example.com"},
		Subject: "Plain subject", BodyText: "Hello\nsecond line\n", BodySource: "text/plain",
		// invoice.pdf's metadata size deliberately differs from the 12 bytes
		// the fake serves: the tool output and the log must report what was
		// downloaded, the cap must use the metadata sum.
		Attachments: []gmail.Attachment{
			{Filename: "invoice.pdf", MimeType: "application/pdf", SizeBytes: 999, AttachmentID: "att-1"},
			{Filename: "notes.txt", MimeType: "text/plain", SizeBytes: 5, AttachmentID: "att-2"},
		},
	}
	e.work.attachments = map[string][]byte{"att-1": []byte("%PDF-1.4 abc"), "att-2": []byte("notes")}
	return e
}

type rawPart struct {
	ctype string
	name  string
	body  string
}

// parseUpload splits the uploaded raw message into its headers and parts.
func parseUpload(t *testing.T, raw []byte) (mail.Header, []rawPart) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	mt, params, err := stdmime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mt != "multipart/mixed" {
		body, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
		return msg.Header, []rawPart{{ctype: mt, body: string(body)}}
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []rawPart
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		ct, _, _ := stdmime.ParseMediaType(p.Header.Get("Content-Type"))
		_, dp, _ := stdmime.ParseMediaType(p.Header.Get("Content-Disposition"))
		var body []byte
		switch enc := p.Header.Get("Content-Transfer-Encoding"); enc {
		case "base64":
			body, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		case "8bit":
			// message/rfc822 travels verbatim (RFC 2046 5.2.1).
			body, err = io.ReadAll(p)
		default:
			t.Fatalf("part encoding = %q", enc)
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts = append(parts, rawPart{ctype: ct, name: dp["filename"], body: string(body)})
	}
	return msg.Header, parts
}

func TestForwardInlineHappyPath(t *testing.T) {
	e := forwardFixture(t)
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{
		MessageID: " m-1 ", To: []string{"dave@example.com"}, Cc: []string{"erin@example.com"}, BodyText: "FYI",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The leading/trailing space in MessageID must not reach the service:
	// the call list shows the trimmed id, same as the batch tool.
	if strings.Join(e.work.calls, ",") != "headers:m-1,message:m-1,att:att-1,att:att-2,upload,send:d-2" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	if out.Account != "work" || out.Mode != "inline" || out.ForwardedMessageID != "m-1" || !out.Sent || out.DraftID != "" || out.MessageID != "m-9" || out.ThreadID != "t-9" {
		t.Fatalf("out = %+v", out)
	}
	if out.Subject != "Fwd: Plain subject" || strings.Join(out.To, ",") != "<dave@example.com>" || strings.Join(out.Cc, ",") != "<erin@example.com>" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Attachments) != 2 || out.Attachments[0].Filename != "invoice.pdf" || out.Attachments[0].SizeBytes != 12 || out.Attachments[1].MimeType != "text/plain" {
		t.Fatalf("attachments = %+v", out.Attachments)
	}
	if e.work.uploadThread != "t-orig" {
		t.Fatalf("thread = %q", e.work.uploadThread)
	}
	hdr, parts := parseUpload(t, e.work.uploadRaw)
	if hdr.Get("In-Reply-To") != "<orig@mail.example.com>" || hdr.Get("References") != "<root@mail.example.com> <orig@mail.example.com>" {
		t.Fatalf("threading headers = %+v", hdr)
	}
	if hdr.Get("From") != "control@example.com" || hdr.Get("Subject") != "Fwd: Plain subject" {
		t.Fatalf("headers = %+v", hdr)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	wantBody := "FYI\n\n" +
		"---------- Forwarded message ---------\n" +
		"From: Ann <ann@example.com>\n" +
		"Date: " + formatDate(time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)) + "\n" +
		"Subject: Plain subject\n" +
		"To: control@example.com, bob@example.com\n" +
		"Cc: carol@example.com\n" +
		"\n" +
		"Hello\nsecond line\n"
	if parts[0].ctype != "text/plain" || parts[0].body != wantBody {
		t.Fatalf("text part:\n%q\nwant:\n%q", parts[0].body, wantBody)
	}
	if parts[1].name != "invoice.pdf" || parts[1].ctype != "application/pdf" || parts[1].body != "%PDF-1.4 abc" {
		t.Fatalf("part 1 = %+v", parts[1])
	}
	if parts[2].name != "notes.txt" || parts[2].body != "notes" {
		t.Fatalf("part 2 = %+v", parts[2])
	}
	if !strings.Contains(e.logs.String(), "forward sent") || strings.Contains(e.logs.String(), "second line") {
		t.Fatalf("logs = %s", e.logs.String())
	}
	if strings.Contains(e.logs.String(), "forward draft created") {
		t.Fatalf("logs = %s, want no draft-created line on the default send path", e.logs.String())
	}
	// bytes counts what was downloaded (12 + 5), not the metadata sum.
	if !strings.Contains(e.logs.String(), "bytes=17") {
		t.Fatalf("logs = %s, want bytes=17", e.logs.String())
	}
}

// TestForwardDraftOnly proves draft_only keeps the old draft-only behavior:
// no send, the draft's own ids, and the "forward draft created" log line.
func TestForwardDraftOnly(t *testing.T) {
	e := forwardFixture(t)
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{
		MessageID: "m-1", To: []string{"dave@example.com"}, DraftOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1,message:m-1,att:att-1,att:att-2,upload" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	if out.Sent {
		t.Fatalf("out.Sent = true, want false")
	}
	if out.DraftID != "d-2" || out.MessageID != "m-10" || out.ThreadID != "t-10" {
		t.Fatalf("out = %+v", out)
	}
	if !strings.Contains(e.logs.String(), "forward draft created") {
		t.Fatalf("logs = %s", e.logs.String())
	}
}

// TestForwardSendFailureNamesDraft proves a send failure after the draft
// was created names the draft in the error so the caller can retry with
// send_draft instead of forwarding again.
func TestForwardSendFailureNamesDraft(t *testing.T) {
	e := forwardFixture(t)
	e.work.sendErr = errors.New("smtp down")
	_, _, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{
		MessageID: "m-1", To: []string{"dave@example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "forward draft d-2 created but sending failed") || !strings.Contains(err.Error(), "smtp down") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(e.logs.String(), "forward send failed") || !strings.Contains(e.logs.String(), "d-2") {
		t.Fatalf("logs = %s", e.logs.String())
	}
}

func TestForwardInlineNoNoteNoCcNoAttachments(t *testing.T) {
	e := forwardFixture(t)
	e.work.message.Cc = nil
	e.work.message.Attachments = nil
	e.work.message.BodyText = ""
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"dave@example.com"}, DraftOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1,message:m-1,upload" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	if out.Attachments == nil || len(out.Attachments) != 0 {
		t.Fatalf("attachments must be an empty, non-nil list: %#v", out.Attachments)
	}
	_, parts := parseUpload(t, e.work.uploadRaw)
	if len(parts) != 1 {
		t.Fatalf("parts = %d, want single part without attachments", len(parts))
	}
	wantBody := "---------- Forwarded message ---------\n" +
		"From: Ann <ann@example.com>\n" +
		"Date: " + formatDate(time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)) + "\n" +
		"Subject: Plain subject\n" +
		"To: control@example.com, bob@example.com\n" +
		"\n"
	if parts[0].body != wantBody {
		t.Fatalf("body:\n%q\nwant:\n%q", parts[0].body, wantBody)
	}
}

func TestForwardSubjectRules(t *testing.T) {
	e := forwardFixture(t)
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}, Subject: "Custom"})
	if err != nil || out.Subject != "Custom" {
		t.Fatalf("out = %+v err %v", out, err)
	}
	e.work.headers.Subject = "FW: already"
	_, out, err = e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}})
	if err != nil || out.Subject != "FW: already" {
		t.Fatalf("out = %+v err %v", out, err)
	}
}

func TestForwardCcDedupe(t *testing.T) {
	e := forwardFixture(t)
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{
		MessageID: "m-1", To: []string{"dave@example.com"}, Cc: []string{"Dave <dave@example.com>", "control@example.com", "erin@example.com", "erin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Cc, ",") != "<erin@example.com>" {
		t.Fatalf("cc = %v (to, self and duplicates must be dropped)", out.Cc)
	}
}

func TestForwardInlineSizeCap(t *testing.T) {
	e := forwardFixture(t)
	old := maxForwardBytes
	maxForwardBytes = 16
	t.Cleanup(func() { maxForwardBytes = old })
	_, _, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}})
	if err == nil || err.Error() != "attachments total 1004 bytes, above the 100 MB limit" {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1,message:m-1" {
		t.Fatalf("calls = %v (nothing may be downloaded or uploaded)", e.work.calls)
	}
}

func TestForwardValidation(t *testing.T) {
	e := forwardFixture(t)
	cases := []struct {
		name string
		in   ForwardMessageInput
		want string
	}{
		{"missing message_id", ForwardMessageInput{To: []string{"d@example.com"}}, "message_id is required"},
		{"missing to", ForwardMessageInput{MessageID: "m-1"}, "to is required"},
		{"blank to", ForwardMessageInput{MessageID: "m-1", To: []string{" "}}, "to is required"},
		{"bad to", ForwardMessageInput{MessageID: "m-1", To: []string{"not an address"}}, "invalid email address"},
		{"bad cc", ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}, Cc: []string{"nope"}}, "invalid email address"},
		{"bad bcc", ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}, Bcc: []string{"nope"}}, "invalid email address"},
	}
	for _, c := range cases {
		_, _, err := e.h.forwardMessage(context.Background(), nil, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if len(e.asked) != 0 {
		t.Fatalf("validation must fail before any service is built: %v", e.asked)
	}
}

func TestForwardOriginalNotFound(t *testing.T) {
	e := forwardFixture(t)
	e.work.headers = nil
	_, _, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-gone", To: []string{"d@example.com"}})
	if err == nil || !strings.Contains(err.Error(), "not found: m-gone") {
		t.Fatalf("err = %v", err)
	}
}

// TestForwardInlineAttachmentData covers Gmail delivering a small part's
// bytes inline: the attachment is forwarded without any download call.
func TestForwardInlineAttachmentData(t *testing.T) {
	e := forwardFixture(t)
	e.work.message.Attachments = []gmail.Attachment{
		{Filename: "tiny.txt", MimeType: "text/plain", SizeBytes: 12, Data: []byte("inline bytes")},
	}
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}, DraftOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1,message:m-1,upload" {
		t.Fatalf("calls = %v (inline bytes must not be downloaded)", e.work.calls)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].Filename != "tiny.txt" || out.Attachments[0].SizeBytes != 12 {
		t.Fatalf("attachments = %+v", out.Attachments)
	}
	_, parts := parseUpload(t, e.work.uploadRaw)
	if len(parts) != 2 || parts[1].name != "tiny.txt" || parts[1].body != "inline bytes" {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestForwardAttachmentWithoutIDOrData(t *testing.T) {
	e := forwardFixture(t)
	e.work.message.Attachments = []gmail.Attachment{{Filename: "tiny.txt", MimeType: "text/plain", SizeBytes: 1}}
	_, _, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}})
	if err == nil || !strings.Contains(err.Error(), `attachment "tiny.txt" has no attachment id`) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(strings.Join(e.work.calls, ","), "upload") {
		t.Fatal("no draft may be created")
	}
}

func TestForwardUsesRequestedAccount(t *testing.T) {
	e := forwardFixture(t)
	e.personal.headers = e.work.headers
	e.personal.message = e.work.message
	e.personal.attachments = e.work.attachments
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{Account: "personal", MessageID: "m-1", To: []string{"d@example.com"}, DraftOnly: true})
	if err != nil || out.Account != "personal" || out.From != "p@gmail.com" {
		t.Fatalf("out = %+v err %v", out, err)
	}
	if len(e.work.calls) != 0 {
		t.Fatalf("work must be untouched: %v", e.work.calls)
	}
	hdr, _ := parseUpload(t, e.personal.uploadRaw)
	if hdr.Get("From") != "p@gmail.com" {
		t.Fatalf("From = %q", hdr.Get("From"))
	}
}

func TestForwardEmlHappyPath(t *testing.T) {
	e := forwardFixture(t)
	e.work.rawMessage = []byte("From: ann@example.com\r\nSubject: Plain subject\r\n\r\noriginal body\r\n")
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"dave@example.com"}, AsEml: true, DraftOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1,raw:m-1,upload" {
		t.Fatalf("calls = %v (inline fetches must not happen)", e.work.calls)
	}
	if out.Mode != "eml" || out.Subject != "Fwd: Plain subject" || out.ForwardedMessageID != "m-1" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].Filename != "Plain subject.eml" || out.Attachments[0].MimeType != "message/rfc822" || out.Attachments[0].SizeBytes != int64(len(e.work.rawMessage)) {
		t.Fatalf("attachments = %+v", out.Attachments)
	}
	if e.work.uploadThread != "t-orig" {
		t.Fatalf("thread = %q", e.work.uploadThread)
	}
	hdr, parts := parseUpload(t, e.work.uploadRaw)
	if hdr.Get("In-Reply-To") != "<orig@mail.example.com>" {
		t.Fatalf("threading headers = %+v", hdr)
	}
	if len(parts) != 2 || parts[0].body != "Forwarded message attached." {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].ctype != "message/rfc822" || parts[1].name != "Plain subject.eml" || parts[1].body != string(e.work.rawMessage) {
		t.Fatalf("eml part = %+v", parts[1])
	}
	if strings.Contains(string(e.work.uploadRaw), base64.StdEncoding.EncodeToString(e.work.rawMessage)) {
		t.Fatal("the original must be attached verbatim, not base64 encoded")
	}
}

// TestForwardEmlSendsByDefault proves eml mode sends by default, same as
// inline mode: no draft_only means the draft gets sent immediately.
func TestForwardEmlSendsByDefault(t *testing.T) {
	e := forwardFixture(t)
	e.work.rawMessage = []byte("From: ann@example.com\r\nSubject: Plain subject\r\n\r\noriginal body\r\n")
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"dave@example.com"}, AsEml: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1,raw:m-1,upload,send:d-2" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	if !out.Sent || out.Mode != "eml" || out.DraftID != "" {
		t.Fatalf("out = %+v", out)
	}
	if !strings.Contains(e.logs.String(), "forward sent") || !strings.Contains(e.logs.String(), "mode=eml") {
		t.Fatalf("logs = %s", e.logs.String())
	}
}

func TestForwardEmlNoteAndFilename(t *testing.T) {
	e := forwardFixture(t)
	e.work.rawMessage = []byte("From: ann@example.com\r\n\r\nx\r\n")
	e.work.headers.Subject = "Re: a/b\\c"
	_, out, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"dave@example.com"}, AsEml: true, BodyText: "Here is the original."})
	if err != nil {
		t.Fatal(err)
	}
	if out.Attachments[0].Filename != "Re_ a_b_c.eml" {
		t.Fatalf("filename = %q", out.Attachments[0].Filename)
	}
	_, parts := parseUpload(t, e.work.uploadRaw)
	if parts[0].body != "Here is the original." {
		t.Fatalf("body = %q", parts[0].body)
	}
	e.work.headers.Subject = ""
	_, out, err = e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"dave@example.com"}, AsEml: true})
	if err != nil || out.Attachments[0].Filename != "forwarded-message.eml" {
		t.Fatalf("out = %+v err %v", out, err)
	}
}

func TestForwardEmlSizeCap(t *testing.T) {
	old := maxForwardBytes
	maxForwardBytes = 16
	t.Cleanup(func() { maxForwardBytes = old })
	in := ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}, AsEml: true}

	// The size estimate from the headers call stops the forward before the
	// raw download, which is what the cap promises.
	t.Run("estimate", func(t *testing.T) {
		e := forwardFixture(t)
		e.work.headers.SizeEstimate = 5000
		e.work.rawMessage = []byte(strings.Repeat("x", 40))
		_, _, err := e.h.forwardMessage(context.Background(), nil, in)
		if err == nil || err.Error() != "original message is 5000 bytes, above the 100 MB limit" {
			t.Fatalf("err = %v", err)
		}
		if strings.Join(e.work.calls, ",") != "headers:m-1" {
			t.Fatalf("calls = %v (the raw message must not be downloaded)", e.work.calls)
		}
	})

	// An estimate under the cap still gets checked against the exact length.
	t.Run("exact length", func(t *testing.T) {
		e := forwardFixture(t)
		e.work.rawMessage = []byte(strings.Repeat("x", 40))
		_, _, err := e.h.forwardMessage(context.Background(), nil, in)
		if err == nil || err.Error() != "original message is 40 bytes, above the 100 MB limit" {
			t.Fatalf("err = %v", err)
		}
		if strings.Join(e.work.calls, ",") != "headers:m-1,raw:m-1" {
			t.Fatalf("calls = %v (no draft may be created)", e.work.calls)
		}
	})
}

func TestForwardEmlRawNotFound(t *testing.T) {
	e := forwardFixture(t)
	e.work.rawMessage = nil
	_, _, err := e.h.forwardMessage(context.Background(), nil, ForwardMessageInput{MessageID: "m-1", To: []string{"d@example.com"}, AsEml: true})
	if err == nil || !strings.Contains(err.Error(), "not found: m-1") {
		t.Fatalf("err = %v", err)
	}
}
