package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// fakeService records calls and returns canned data.
type fakeService struct {
	email       string
	calls       []string
	searchParam gmail.SearchParams
	rawDraft    []byte
	draftThread string
	modifyAdd   []string
	modifyRem   []string
	headers     *gmail.MessageHeaders
	headersByID map[string]*gmail.MessageHeaders
	failWith    error
	sendErr     error

	// forward support
	message      *gmail.Message    // returned by GetMessage when set
	attachments  map[string][]byte // attachment id -> bytes
	rawMessage   []byte            // returned by GetRawMessage
	uploads      [][]byte          // every CreateDraftUpload body, in order
	uploadRaw    []byte
	uploadThread string
}

func (f *fakeService) rec(s string)  { f.calls = append(f.calls, s) }
func (f *fakeService) Email() string { return f.email }
func (f *fakeService) SearchThreads(_ context.Context, p gmail.SearchParams) (*gmail.SearchResult, error) {
	f.rec("search")
	f.searchParam = p
	if f.failWith != nil {
		return nil, f.failWith
	}
	return &gmail.SearchResult{NextPageToken: "np", Threads: []gmail.ThreadSummary{{
		ThreadID: "t-1", MessageCount: 2, LastMessageAt: time.UnixMilli(1700000000000), From: "ann@example.com",
		Subject: "S", Snippet: "sn", Labels: []string{"INBOX"}, Unread: true,
	}}}, nil
}
func (f *fakeService) GetThread(_ context.Context, id string, max int) (*gmail.Thread, error) {
	f.rec("thread:" + id)
	return &gmail.Thread{ThreadID: id, Messages: []gmail.Message{{
		MessageID: "m-1", ThreadID: id, Date: time.UnixMilli(1700000000000), From: "ann@example.com",
		To: []string{"control@example.com"}, Subject: "S", Labels: []string{"INBOX"}, BodyText: strings.Repeat("x", max),
		BodyTruncated: true, BodySource: "text/plain",
		Attachments: []gmail.Attachment{{Filename: "a.pdf", MimeType: "application/pdf", SizeBytes: 3, AttachmentID: "att"}},
	}}}, nil
}
func (f *fakeService) GetMessage(_ context.Context, id string, max int) (*gmail.Message, error) {
	f.rec("message:" + id)
	if f.failWith != nil {
		return nil, f.failWith
	}
	if f.message != nil {
		return f.message, nil
	}
	return &gmail.Message{MessageID: id, ThreadID: "t-1", BodySource: "none"}, nil
}
func (f *fakeService) GetAttachment(_ context.Context, msgID, attID string) ([]byte, error) {
	f.rec("att:" + attID)
	data, ok := f.attachments[attID]
	if !ok {
		return nil, &gmail.NotFoundError{ID: attID}
	}
	return data, nil
}
func (f *fakeService) GetRawMessage(_ context.Context, id string) ([]byte, error) {
	f.rec("raw:" + id)
	if f.rawMessage == nil {
		return nil, &gmail.NotFoundError{ID: id}
	}
	return f.rawMessage, nil
}
func (f *fakeService) CreateDraftUpload(_ context.Context, raw []byte, threadID string) (*gmail.DraftResult, error) {
	f.rec("upload")
	f.uploads = append(f.uploads, raw)
	f.uploadRaw, f.uploadThread = raw, threadID
	return &gmail.DraftResult{DraftID: "d-2", MessageID: "m-10", ThreadID: "t-10"}, nil
}
func (f *fakeService) GetMessageHeaders(_ context.Context, id string) (*gmail.MessageHeaders, error) {
	f.rec("headers:" + id)
	// headersByID serves a batch: each message id gets its own headers, an
	// unknown id is a 404. Without it the single canned value answers.
	if f.headersByID != nil {
		hd, ok := f.headersByID[id]
		if !ok {
			return nil, &gmail.NotFoundError{ID: id}
		}
		return hd, nil
	}
	if f.headers == nil {
		return nil, &gmail.NotFoundError{ID: id}
	}
	return f.headers, nil
}
func (f *fakeService) ListLabels(context.Context) ([]gmail.Label, error) {
	f.rec("labels")
	return []gmail.Label{{ID: "INBOX", Name: "INBOX", Type: "system"}}, nil
}
func (f *fakeService) ModifyThreadLabels(_ context.Context, id string, add, remove []string) ([]string, error) {
	f.rec("modify:" + id)
	f.modifyAdd, f.modifyRem = add, remove
	return []string{"STARRED"}, nil
}
func (f *fakeService) CreateDraft(_ context.Context, raw []byte, threadID string) (*gmail.DraftResult, error) {
	f.rec("draft")
	f.rawDraft, f.draftThread = raw, threadID
	return &gmail.DraftResult{DraftID: "d-1", MessageID: "m-9", ThreadID: "t-9"}, nil
}
func (f *fakeService) SendDraft(_ context.Context, id string) (*gmail.SendResult, error) {
	f.rec("send:" + id)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &gmail.SendResult{MessageID: "m-9", ThreadID: "t-9", To: []string{"bob@example.com"}, Cc: []string{"carol@example.com"}, Subject: "Sent subject"}, nil
}
func (f *fakeService) DeleteDraft(_ context.Context, id string) error {
	f.rec("delete:" + id)
	return nil
}
func (f *fakeService) TrashThread(_ context.Context, id string) ([]string, error) {
	f.rec("trash:" + id)
	return []string{"TRASH"}, nil
}
func (f *fakeService) UntrashThread(_ context.Context, id string) ([]string, error) {
	f.rec("untrash:" + id)
	return []string{"INBOX"}, nil
}

type env struct {
	h        *handlers
	work     *fakeService
	personal *fakeService
	asked    []string
	logs     *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{work: &fakeService{email: "control@example.com"}, personal: &fakeService{email: "p@gmail.com"}, logs: &bytes.Buffer{}}
	cfg := &config.File{Version: 1, Default: "work", Accounts: []config.Account{
		{Alias: "work", Email: "control@example.com", Description: "the company mailbox; use it by default"},
		{Alias: "personal", Email: "p@gmail.com", Description: "the user's personal mailbox; use it when they say personal"},
	}}
	e.h = &handlers{d: Deps{
		Config: func() (*config.File, error) { return cfg, nil },
		Service: func(_ context.Context, alias string) (gmail.Service, error) {
			e.asked = append(e.asked, alias)
			switch alias {
			case "work":
				return e.work, nil
			case "personal":
				return e.personal, nil
			}
			return nil, errors.New("no service for " + alias)
		},
		Logger: slog.New(slog.NewTextHandler(e.logs, nil)),
		Now:    func() time.Time { return time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) },
	}}
	return e
}

// listTools registers the tools on an in-memory server and lists them
// through a client session, the way Claude Code sees them.
func listTools(t *testing.T, e *env) []*mcp.Tool {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, e.h.d)

	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return list.Tools
}

// accountPropertyDescription reads properties.account.description out of a
// listed tool's input schema, whatever concrete type carries it.
func accountPropertyDescription(t *testing.T, tool *mcp.Tool) string {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("%s: marshal schema: %v", tool.Name, err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("%s: parse schema %s: %v", tool.Name, raw, err)
	}
	prop, ok := schema.Properties["account"]
	if !ok {
		t.Fatalf("%s: schema has no account property: %s", tool.Name, raw)
	}
	return prop.Description
}

func TestAccountDescriptionOnEveryInput(t *testing.T) {
	e := newEnv(t)
	cfg, err := e.h.d.Config()
	if err != nil {
		t.Fatal(err)
	}
	want := AccountDescription(cfg)
	if !strings.Contains(want, "the company mailbox; use it by default") ||
		!strings.Contains(want, "the user's personal mailbox; use it when they say personal") {
		t.Fatalf("rendered description = %q", want)
	}

	tools := listTools(t, e)
	withAccount := 0
	for _, tool := range tools {
		if tool.Name == "accounts_list" {
			continue
		}
		if got := accountPropertyDescription(t, tool); got != want {
			t.Errorf("%s account description = %q, want %q", tool.Name, got, want)
		}
		withAccount++
	}
	if withAccount != len(ToolNames)-1 {
		t.Fatalf("tools with an account property = %d, want %d", withAccount, len(ToolNames)-1)
	}

	// The struct tags carry the generic text used when the config cannot
	// be read, so the two must stay identical.
	inputs := []any{SearchThreadsInput{}, GetThreadInput{}, GetMessageInput{}, ListLabelsInput{}, ModifyLabelsInput{},
		CreateDraftInput{}, SendDraftInput{}, DeleteDraftInput{}, TrashThreadInput{}, UntrashThreadInput{}, ForwardMessageInput{},
		ForwardMessagesInput{}}
	for _, in := range inputs {
		f, ok := reflect.TypeOf(in).FieldByName("Account")
		if !ok {
			t.Fatalf("%T has no Account field", in)
		}
		if f.Tag.Get("json") != "account,omitempty" || f.Tag.Get("jsonschema") != genericAccountDescription {
			t.Fatalf("%T Account tag = %q", in, f.Tag)
		}
	}
	if len(ToolNames) != 13 {
		t.Fatalf("ToolNames = %d", len(ToolNames))
	}
}

func TestAccountDescriptionFallsBackWhenConfigFails(t *testing.T) {
	e := newEnv(t)
	e.h.d.Config = func() (*config.File, error) { return nil, errors.New("accounts.json is unreadable") }
	for _, tool := range listTools(t, e) {
		if tool.Name == "accounts_list" {
			continue
		}
		if got := accountPropertyDescription(t, tool); got != genericAccountDescription {
			t.Fatalf("%s account description = %q, want the generic text", tool.Name, got)
		}
	}
}

func TestAccountDescriptionRendering(t *testing.T) {
	described := &config.File{Version: 1, Default: "work", Accounts: []config.Account{
		{Alias: "work", Email: "you@company.example", Description: "the company mailbox; use it by default"},
		{Alias: "personal", Email: "you@gmail.com", Description: "the personal mailbox; use it when they say personal."},
	}}
	want := "Account alias. Default 'work' (you@company.example). " +
		"'work' (you@company.example): the company mailbox; use it by default. " +
		"'personal' (you@gmail.com): the personal mailbox; use it when they say personal. " +
		"Pick the account from the user's wording and never ask which one."
	if got := AccountDescription(described); got != want {
		t.Fatalf("with descriptions:\n got %q\nwant %q", got, want)
	}

	bare := &config.File{Version: 1, Default: "work", Accounts: []config.Account{
		{Alias: "work", Email: "you@company.example"},
		{Alias: "personal", Email: "you@gmail.com"},
	}}
	want = "Account alias. Default 'work' (you@company.example). " +
		"'work' (you@company.example): use it when the user names this alias or address. " +
		"'personal' (you@gmail.com): use it when the user names this alias or address. " +
		"Pick the account from the user's wording and never ask which one."
	if got := AccountDescription(bare); got != want {
		t.Fatalf("without descriptions:\n got %q\nwant %q", got, want)
	}

	want = "Account alias. No accounts are configured; run minuar-multi-gmail add-account."
	if got := AccountDescription(&config.File{Version: 1}); got != want {
		t.Fatalf("no accounts:\n got %q\nwant %q", got, want)
	}
	if got := AccountDescription(nil); got != want {
		t.Fatalf("nil config:\n got %q\nwant %q", got, want)
	}
}

func TestAccountSentencesNilConfigDoesNotPanic(t *testing.T) {
	if got := AccountSentences(nil); got != noAccountsDescription {
		t.Fatalf("AccountSentences(nil) = %q, want %q", got, noAccountsDescription)
	}
}

func TestRegisterDoesNotPanic(t *testing.T) {
	e := newEnv(t)
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, e.h.d)
}

// TestSendingToolsRequireUserInteraction lists the registered tools through
// an in-memory client session and checks that forward_message,
// forward_messages and send_draft, and only those three, carry the
// approval-prompt meta flag.
func TestSendingToolsRequireUserInteraction(t *testing.T) {
	e := newEnv(t)
	want := map[string]bool{"forward_message": true, "forward_messages": true, "send_draft": true}
	seen := map[string]bool{}
	for _, tool := range listTools(t, e) {
		seen[tool.Name] = true
		got, _ := tool.Meta["anthropic/requiresUserInteraction"].(bool)
		if got != want[tool.Name] {
			t.Errorf("%s: Meta[anthropic/requiresUserInteraction] = %v, want %v", tool.Name, got, want[tool.Name])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("seen = %v, want %s listed", seen, name)
		}
	}
}

func TestAccountsList(t *testing.T) {
	e := newEnv(t)
	_, out, err := e.h.accountsList(context.Background(), nil, struct{}{})
	if err != nil || out.Default != "work" || len(out.Accounts) != 2 || out.Accounts[1].Email != "p@gmail.com" {
		t.Fatalf("out = %+v err %v", out, err)
	}
}

func TestSearchDefaultsToWorkAndBounds(t *testing.T) {
	e := newEnv(t)
	_, out, err := e.h.searchThreads(context.Background(), nil, SearchThreadsInput{Query: "from:ann"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.asked, ",") != "work" || out.Account != "work" || out.Email != "control@example.com" {
		t.Fatalf("asked = %v out = %+v", e.asked, out)
	}
	if e.work.searchParam.MaxResults != 10 || e.work.searchParam.Query != "from:ann" {
		t.Fatalf("params = %+v", e.work.searchParam)
	}
	row := out.Threads[0]
	if row.ThreadID != "t-1" || !row.Unread || row.LastMessageAt != time.UnixMilli(1700000000000).In(time.Local).Format(time.RFC3339) {
		t.Fatalf("row = %+v", row)
	}
	if out.NextPageToken != "np" {
		t.Fatalf("out = %+v", out)
	}
	if _, _, err := e.h.searchThreads(context.Background(), nil, SearchThreadsInput{Query: "x", MaxResults: 51}); err == nil {
		t.Fatal("max_results 51 accepted")
	}
	if _, _, err := e.h.searchThreads(context.Background(), nil, SearchThreadsInput{Query: "  "}); err == nil {
		t.Fatal("empty query accepted")
	}
	_, out, _ = e.h.searchThreads(context.Background(), nil, SearchThreadsInput{Account: "personal", Query: "x", MaxResults: 50, PageToken: "pt", IncludeSpamTrash: true})
	if out.Account != "personal" || e.personal.searchParam.MaxResults != 50 || e.personal.searchParam.PageToken != "pt" || !e.personal.searchParam.IncludeSpamTrash {
		t.Fatalf("personal params = %+v", e.personal.searchParam)
	}
}

func TestUnknownAccountListsAliasesWithoutCallingService(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.h.searchThreads(context.Background(), nil, SearchThreadsInput{Account: "nope", Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "personal") || !strings.Contains(err.Error(), "work") {
		t.Fatalf("err = %v", err)
	}
	if len(e.asked) != 0 {
		t.Fatal("service must not be built for an unknown alias")
	}
}

func TestServiceErrorIsReturnedAsToolError(t *testing.T) {
	e := newEnv(t)
	e.work.failWith = errors.New("boom")
	_, _, err := e.h.searchThreads(context.Background(), nil, SearchThreadsInput{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetThreadAndMessage(t *testing.T) {
	e := newEnv(t)
	_, out, err := e.h.getThread(context.Background(), nil, GetThreadInput{ThreadID: "t-1"})
	if err != nil || out.ThreadID != "t-1" || len(out.Messages) != 1 {
		t.Fatalf("out = %+v err %v", out, err)
	}
	m := out.Messages[0]
	if len(m.BodyText) != 5000 || !m.BodyTruncated || m.BodySource != "text/plain" || m.Attachments[0].Filename != "a.pdf" {
		t.Fatalf("message = %+v", m)
	}
	if m.Date != time.UnixMilli(1700000000000).In(time.Local).Format(time.RFC3339) {
		t.Fatalf("date = %q", m.Date)
	}
	if _, _, err := e.h.getThread(context.Background(), nil, GetThreadInput{ThreadID: "t-1", MaxBodyChars: 100001}); err == nil {
		t.Fatal("max_body_chars 100001 accepted")
	}
	if _, _, err := e.h.getThread(context.Background(), nil, GetThreadInput{}); err == nil {
		t.Fatal("empty thread_id accepted")
	}
	_, mo, err := e.h.getMessage(context.Background(), nil, GetMessageInput{MessageID: "m-1", MaxBodyChars: 7})
	if err != nil || mo.Message.MessageID != "m-1" || mo.Message.BodySource != "none" {
		t.Fatalf("out = %+v err %v", mo, err)
	}
}

func TestLabels(t *testing.T) {
	e := newEnv(t)
	_, lo, err := e.h.listLabels(context.Background(), nil, ListLabelsInput{})
	if err != nil || len(lo.Labels) != 1 || lo.Labels[0].Name != "INBOX" {
		t.Fatalf("out = %+v err %v", lo, err)
	}
	_, mo, err := e.h.modifyLabels(context.Background(), nil, ModifyLabelsInput{ThreadID: "t-1", AddLabels: []string{"STARRED"}, RemoveLabels: []string{"UNREAD"}})
	if err != nil || strings.Join(mo.LabelsAfter, ",") != "STARRED" || mo.ThreadID != "t-1" {
		t.Fatalf("out = %+v err %v", mo, err)
	}
	if strings.Join(e.work.modifyAdd, ",") != "STARRED" || strings.Join(e.work.modifyRem, ",") != "UNREAD" {
		t.Fatalf("add=%v rem=%v", e.work.modifyAdd, e.work.modifyRem)
	}
	if _, _, err := e.h.modifyLabels(context.Background(), nil, ModifyLabelsInput{ThreadID: "t-1"}); err == nil {
		t.Fatal("no labels accepted")
	}
	if _, _, err := e.h.modifyLabels(context.Background(), nil, ModifyLabelsInput{AddLabels: []string{"X"}}); err == nil {
		t.Fatal("empty thread_id accepted")
	}
}

func TestCreateDraftNew(t *testing.T) {
	e := newEnv(t)
	in := CreateDraftInput{To: []string{"bob@example.com"}, Cc: []string{"carol@example.com"}, Bcc: []string{"dan@example.com"}, Subject: "Hi", BodyText: "Body"}
	_, out, err := e.h.createDraft(context.Background(), nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.DraftID != "d-1" || out.From != "control@example.com" || out.Subject != "Hi" || strings.Join(out.Bcc, ",") != "<dan@example.com>" {
		t.Fatalf("out = %+v", out)
	}
	raw := string(e.work.rawDraft)
	if !strings.Contains(raw, "From: control@example.com\r\n") || !strings.Contains(raw, "Subject: Hi\r\n") || !strings.Contains(raw, "To: <bob@example.com>\r\n") {
		t.Fatalf("raw = %q", raw)
	}
	if e.work.draftThread != "" {
		t.Fatalf("new draft must not carry a thread id, got %q", e.work.draftThread)
	}
	if !strings.Contains(e.logs.String(), "draft created") || strings.Contains(e.logs.String(), "Body") {
		t.Fatalf("logs = %s", e.logs.String())
	}
	for name, bad := range map[string]CreateDraftInput{
		"no to":      {Subject: "Hi", BodyText: "b"},
		"no subject": {To: []string{"bob@example.com"}, BodyText: "b"},
		"no body":    {To: []string{"bob@example.com"}, Subject: "s"},
		"bad addr":   {To: []string{"nope"}, Subject: "s", BodyText: "b"},
	} {
		if _, _, err := e.h.createDraft(context.Background(), nil, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCreateDraftReply(t *testing.T) {
	e := newEnv(t)
	e.work.headers = &gmail.MessageHeaders{
		MessageID: "m-orig", ThreadID: "t-orig", RFCMessageID: "<orig@mail.example.com>", References: []string{"<root@mail.example.com>"},
		Subject: "Plain subject", From: "Ann <ann@example.com>", To: []string{"control@example.com", "bob@example.com"}, Cc: []string{"carol@example.com"},
	}
	_, out, err := e.h.createDraft(context.Background(), nil, CreateDraftInput{ReplyToMessageID: "m-orig", ReplyAll: true, Cc: []string{"extra@example.com"}, BodyText: "Thanks"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject != "Re: Plain subject" || out.ThreadID != "t-9" {
		t.Fatalf("out = %+v", out)
	}
	if strings.Join(out.To, ",") != `"Ann" <ann@example.com>` {
		t.Fatalf("to = %v", out.To)
	}
	if strings.Join(out.Cc, ",") != "<bob@example.com>,<carol@example.com>,<extra@example.com>" {
		t.Fatalf("cc = %v (self must be excluded, explicit cc appended)", out.Cc)
	}
	raw := string(e.work.rawDraft)
	if !strings.Contains(raw, "In-Reply-To: <orig@mail.example.com>\r\n") || !strings.Contains(raw, "References: <root@mail.example.com> <orig@mail.example.com>\r\n") {
		t.Fatalf("raw = %q", raw)
	}
	if e.work.draftThread != "t-orig" {
		t.Fatalf("thread = %q", e.work.draftThread)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-orig,draft" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	// Explicit subject and explicit to win over the derived ones.
	_, out, err = e.h.createDraft(context.Background(), nil, CreateDraftInput{ReplyToMessageID: "m-orig", To: []string{"x@example.com"}, Subject: "Custom", BodyText: "b"})
	if err != nil || out.Subject != "Custom" || strings.Join(out.To, ",") != "<x@example.com>" || len(out.Cc) != 0 {
		t.Fatalf("out = %+v err %v", out, err)
	}
	// Unknown original message.
	e.work.headers = nil
	if _, _, err := e.h.createDraft(context.Background(), nil, CreateDraftInput{ReplyToMessageID: "m-gone", BodyText: "b"}); err == nil || !strings.Contains(err.Error(), "not found: m-gone") {
		t.Fatalf("err = %v", err)
	}
}

// TestCreateDraftReplyDedupesExplicitCc guards the reply path against an
// explicit cc that repeats the account's own address or a recipient the
// reply-all derivation already placed in To.
func TestCreateDraftReplyDedupesExplicitCc(t *testing.T) {
	e := newEnv(t)
	e.work.headers = &gmail.MessageHeaders{
		MessageID: "m-orig", ThreadID: "t-orig", RFCMessageID: "<orig@mail.example.com>",
		Subject: "Plain subject", From: "Ann <ann@example.com>",
		To: []string{"control@example.com", "bob@example.com"}, Cc: []string{"carol@example.com"},
	}
	_, out, err := e.h.createDraft(context.Background(), nil, CreateDraftInput{
		ReplyToMessageID: "m-orig", ReplyAll: true,
		Cc:       []string{"control@example.com", "Bob <bob@example.com>"},
		BodyText: "Thanks",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Cc, ",") != "<bob@example.com>,<carol@example.com>" {
		t.Fatalf("cc = %v (self must be excluded and duplicate bob must not repeat)", out.Cc)
	}
}

func TestSendDeleteTrash(t *testing.T) {
	e := newEnv(t)
	_, so, err := e.h.sendDraft(context.Background(), nil, SendDraftInput{DraftID: "d-1"})
	if err != nil || so.MessageID != "m-9" || so.Subject != "Sent subject" || strings.Join(so.To, ",") != "bob@example.com" {
		t.Fatalf("out = %+v err %v", so, err)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "mail sent") || !strings.Contains(logs, "d-1") || !strings.Contains(logs, "bob@example.com") || !strings.Contains(logs, "account=work") {
		t.Fatalf("logs = %s", logs)
	}
	if _, _, err := e.h.sendDraft(context.Background(), nil, SendDraftInput{}); err == nil {
		t.Fatal("empty draft_id accepted")
	}
	_, do, err := e.h.deleteDraft(context.Background(), nil, DeleteDraftInput{Account: "personal", DraftID: "d-2"})
	if err != nil || !do.Deleted || do.Account != "personal" || strings.Join(e.personal.calls, ",") != "delete:d-2" {
		t.Fatalf("out = %+v err %v calls %v", do, err, e.personal.calls)
	}
	_, to, err := e.h.trashThread(context.Background(), nil, TrashThreadInput{ThreadID: "t-1"})
	if err != nil || strings.Join(to.LabelsAfter, ",") != "TRASH" {
		t.Fatalf("out = %+v err %v", to, err)
	}
	_, uo, err := e.h.untrashThread(context.Background(), nil, UntrashThreadInput{ThreadID: "t-1"})
	if err != nil || strings.Join(uo.LabelsAfter, ",") != "INBOX" {
		t.Fatalf("out = %+v err %v", uo, err)
	}
}
