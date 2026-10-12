package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	stdmime "mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// writeFile puts data in a fresh temp directory under name and returns the
// absolute path.
func writeFile(t *testing.T, name, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func lowerDraftLimit(t *testing.T, n int64) {
	t.Helper()
	old := maxDraftBytes
	maxDraftBytes = n
	t.Cleanup(func() { maxDraftBytes = old })
}

// webDraftBody and webDraft are a reply the way Gmail's web compose stores
// it: plain and HTML alternatives with the quoted thread.
const webDraftBody = "--000000000000a1b2c3\r\n" +
	"Content-Type: text/plain; charset=\"UTF-8\"\r\n" +
	"\r\n" +
	"Hi Ann,\r\n\r\nThe invoice is attached.\r\n\r\nOn Mon, Ann wrote:\r\n> Please send it.\r\n" +
	"\r\n--000000000000a1b2c3\r\n" +
	"Content-Type: text/html; charset=\"UTF-8\"\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"<div dir=3D\"ltr\">Hi Ann,<br><br>The invoice is attached.</div>\r\n" +
	"\r\n--000000000000a1b2c3--\r\n"

const webDraft = "MIME-Version: 1.0\r\n" +
	"References: <root@mail.example.com> <orig@mail.example.com>\r\n" +
	"In-Reply-To: <orig@mail.example.com>\r\n" +
	"Subject: Re: Invoice\r\n" +
	"From: Control <control@example.com>\r\n" +
	"To: Ann <ann@example.com>\r\n" +
	"Cc: bob@example.com\r\n" +
	"Bcc: dan@example.com\r\n" +
	"Content-Type: multipart/alternative; boundary=\"000000000000a1b2c3\"\r\n" +
	"\r\n" + webDraftBody

// draftEnv is newEnv with one stored draft, d-7, whose message is m-7 in
// thread t-7.
func draftEnv(t *testing.T, raw string) *env {
	t.Helper()
	e := newEnv(t)
	e.work.drafts = map[string]*gmail.DraftRaw{"d-7": {DraftID: "d-7", MessageID: "m-7", ThreadID: "t-7", Raw: []byte(raw)}}
	return e
}

// mixedPart is one part of an updated draft, its body not decoded.
type mixedPart struct {
	ctype string
	name  string
	enc   string
	body  []byte
}

func (p mixedPart) decoded(t *testing.T) string {
	t.Helper()
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(p.body)))
	if err != nil {
		t.Fatalf("decode %s: %v", p.name, err)
	}
	return string(data)
}

// splitMixed reads the multipart/mixed message add_draft_attachment
// uploaded. parseUpload decodes every part; here the first part is the old
// message and must be compared byte for byte.
func splitMixed(t *testing.T, raw []byte) (mail.Header, []mixedPart) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	mt, params, err := stdmime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("Content-Type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []mixedPart
	for {
		p, err := mr.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		_, dp, _ := stdmime.ParseMediaType(p.Header.Get("Content-Disposition"))
		parts = append(parts, mixedPart{ctype: p.Header.Get("Content-Type"), name: dp["filename"], enc: p.Header.Get("Content-Transfer-Encoding"), body: body})
	}
	return msg.Header, parts
}

func TestCreateDraftWithOneAttachment(t *testing.T) {
	e := newEnv(t)
	invoice := writeFile(t, "invoice.pdf", "%PDF-1.4 fake")
	in := CreateDraftInput{To: []string{"bob@example.com"}, Subject: "Invoice", BodyText: "See attached\n", Attachments: []string{invoice}}
	_, out, err := e.h.createDraft(context.Background(), nil, in)
	if err != nil {
		t.Fatal(err)
	}
	// A draft with a file goes through the media upload, not the JSON call.
	if strings.Join(e.work.calls, ",") != "upload" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	if out.DraftID != "d-2" || len(out.Attachments) != 1 || out.Attachments[0] != (AttachmentOut{Filename: "invoice.pdf", MimeType: "application/pdf", SizeBytes: 13}) {
		t.Fatalf("out = %+v", out)
	}
	if e.work.uploadThread != "" {
		t.Fatalf("new draft must not carry a thread id, got %q", e.work.uploadThread)
	}
	header, parts := parseUpload(t, e.work.uploadRaw)
	if header.Get("Subject") != "Invoice" || header.Get("To") != "<bob@example.com>" || header.Get("From") != "control@example.com" {
		t.Fatalf("headers = %+v", header)
	}
	if len(parts) != 2 || parts[0].ctype != "text/plain" || parts[0].body != "See attached\n" {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].ctype != "application/pdf" || parts[1].name != "invoice.pdf" || parts[1].body != "%PDF-1.4 fake" {
		t.Fatalf("attachment = %+v", parts[1])
	}
	// The log names counts, never the path or the content.
	logs := e.logs.String()
	if !strings.Contains(logs, "attachments=1") || strings.Contains(logs, invoice) || strings.Contains(logs, "See attached") {
		t.Fatalf("logs = %s", logs)
	}
}

func TestCreateDraftReplyWithSeveralAttachments(t *testing.T) {
	e := newEnv(t)
	e.work.headers = &gmail.MessageHeaders{
		MessageID: "m-orig", ThreadID: "t-orig", RFCMessageID: "<orig@mail.example.com>", References: []string{"<root@mail.example.com>"},
		Subject: "Invoice", From: "Ann <ann@example.com>", To: []string{"control@example.com"},
	}
	paths := []string{writeFile(t, "invoice.pdf", "%PDF"), writeFile(t, "hours.csv", "a,b\n"), writeFile(t, "Rechnung März.pdf", "%PDF-2")}
	_, out, err := e.h.createDraft(context.Background(), nil, CreateDraftInput{ReplyToMessageID: "m-orig", BodyText: "Attached", Attachments: paths})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "headers:m-orig,upload" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	// The reply keeps its thread and its threading headers.
	if e.work.uploadThread != "t-orig" || out.Subject != "Re: Invoice" || strings.Join(out.To, ",") != `"Ann" <ann@example.com>` {
		t.Fatalf("thread = %q out = %+v", e.work.uploadThread, out)
	}
	header, parts := parseUpload(t, e.work.uploadRaw)
	if header.Get("In-Reply-To") != "<orig@mail.example.com>" || header.Get("References") != "<root@mail.example.com> <orig@mail.example.com>" {
		t.Fatalf("headers = %+v", header)
	}
	if len(parts) != 4 || parts[0].body != "Attached" {
		t.Fatalf("parts = %+v", parts)
	}
	got := ""
	for _, p := range parts[1:] {
		got += p.name + "=" + p.ctype + "=" + p.body + ";"
	}
	if got != "invoice.pdf=application/pdf=%PDF;hours.csv=text/csv=a,b\n;Rechnung März.pdf=application/pdf=%PDF-2;" {
		t.Fatalf("attachments = %q", got)
	}
	// The non-ASCII name travels in the RFC 2231 form.
	if !bytes.Contains(e.work.uploadRaw, []byte("filename*=utf-8''Rechnung%20M%C3%A4rz.pdf")) {
		t.Fatalf("raw = %s", e.work.uploadRaw)
	}
}

// TestDraftAttachmentPathsAreRefused covers both tools: they share the
// check, and a refused path must reach no Gmail call.
func TestDraftAttachmentPathsAreRefused(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "invoice.pdf")
	if err := os.WriteFile(good, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"key.pem": "secret", "noext": "x", "empty.pdf": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.pdf"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ path, want string }{
		"relative":  {"invoice.pdf", "must be absolute"},
		"missing":   {filepath.Join(dir, "gone.pdf"), "no such file"},
		"directory": {filepath.Join(dir, "folder.pdf"), "is a directory"},
		"type":      {filepath.Join(dir, "key.pem"), "type pem is not on the allowed list"},
		"no type":   {filepath.Join(dir, "noext"), "has no file type"},
		"empty":     {filepath.Join(dir, "empty.pdf"), "is empty"},
	}
	if runtime.GOOS != "windows" {
		// A link with an allowed name that points at a file of another type.
		link := filepath.Join(dir, "link.pdf")
		if err := os.Symlink(filepath.Join(dir, "key.pem"), link); err != nil {
			t.Fatal(err)
		}
		cases["symlink"] = struct{ path, want string }{link, "symbolic link"}
	}
	for name, c := range cases {
		e := draftEnv(t, webDraft)
		// The good file comes first: one bad path refuses the whole call.
		paths := []string{good, c.path}
		_, _, err := e.h.createDraft(context.Background(), nil, CreateDraftInput{To: []string{"bob@example.com"}, Subject: "s", BodyText: "b", Attachments: paths})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), c.path) {
			t.Errorf("create_draft %s: err = %v, want %q and the path %q", name, err, c.want, c.path)
		}
		_, _, err = e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", Attachments: paths})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), c.path) {
			t.Errorf("add_draft_attachment %s: err = %v, want %q and the path %q", name, err, c.want, c.path)
		}
		if len(e.asked) != 0 || len(e.work.calls) != 0 {
			t.Errorf("%s: asked = %v calls = %v, want no Gmail call", name, e.asked, e.work.calls)
		}
	}
	e := newEnv(t)
	if _, _, err := e.h.createDraft(context.Background(), nil, CreateDraftInput{To: []string{"bob@example.com"}, Subject: "s", BodyText: "b", Attachments: []string{" "}}); err == nil || !strings.Contains(err.Error(), "attachments[0] is empty") {
		t.Fatalf("blank path: err = %v", err)
	}
}

func TestCreateDraftAttachmentSizeLimit(t *testing.T) {
	lowerDraftLimit(t, 2000)
	e := newEnv(t)
	small := writeFile(t, "small.pdf", strings.Repeat("a", 100))
	big := writeFile(t, "big.pdf", strings.Repeat("b", 1400))
	in := CreateDraftInput{To: []string{"bob@example.com"}, Subject: "s", BodyText: "b", Attachments: []string{small, big}}
	_, _, err := e.h.createDraft(context.Background(), nil, in)
	// 1400 bytes are under the limit on disk and over it after base64, with
	// the first file counted in. The error names the file that crosses it.
	if err == nil || !strings.Contains(err.Error(), "25 MB limit") || !strings.Contains(err.Error(), big) || strings.Contains(err.Error(), small) {
		t.Fatalf("err = %v", err)
	}
	if len(e.asked) != 0 || len(e.work.calls) != 0 {
		t.Fatalf("asked = %v calls = %v, want no Gmail call", e.asked, e.work.calls)
	}
	// The small file alone fits.
	in.Attachments = []string{small}
	if _, _, err := e.h.createDraft(context.Background(), nil, in); err != nil {
		t.Fatal(err)
	}
}

func TestAddDraftAttachmentKeepsTheDraft(t *testing.T) {
	e := draftEnv(t, webDraft)
	invoice := writeFile(t, "invoice.pdf", "%PDF-1.4 fake")
	_, out, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", Attachments: []string{invoice}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "draftraw:d-7,update:d-7" {
		t.Fatalf("calls = %v (the draft is updated, never created again)", e.work.calls)
	}
	// Same draft, same thread; the message id is the one Gmail answers.
	if e.work.updateID != "d-7" || e.work.updateThread != "t-7" {
		t.Fatalf("update id = %q thread = %q", e.work.updateID, e.work.updateThread)
	}
	if out.DraftID != "d-7" || out.MessageID != "m-new" || out.ThreadID != "t-7" || out.Account != "work" || out.SizeBytes != int64(len(e.work.updateRaw)) {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Attachments) != 1 || out.Attachments[0] != (AttachmentOut{Filename: "invoice.pdf", MimeType: "application/pdf", SizeBytes: 13}) {
		t.Fatalf("attachments = %+v", out.Attachments)
	}
	header, parts := splitMixed(t, e.work.updateRaw)
	for name, want := range map[string]string{
		"To": "Ann <ann@example.com>", "Cc": "bob@example.com", "Bcc": "dan@example.com", "Subject": "Re: Invoice",
		"From": "Control <control@example.com>", "In-Reply-To": "<orig@mail.example.com>",
		"References": "<root@mail.example.com> <orig@mail.example.com>",
	} {
		if got := header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	// The plain and the HTML alternative are the bytes the user wrote.
	if parts[0].ctype != `multipart/alternative; boundary="000000000000a1b2c3"` || string(parts[0].body) != webDraftBody {
		t.Fatalf("old message changed: %q\n%q", parts[0].ctype, parts[0].body)
	}
	if parts[1].name != "invoice.pdf" || parts[1].ctype != "application/pdf; name=invoice.pdf" || parts[1].enc != "base64" || parts[1].decoded(t) != "%PDF-1.4 fake" {
		t.Fatalf("attachment = %+v", parts[1])
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "draft attachments added") || !strings.Contains(logs, "draft_id=d-7") || strings.Contains(logs, invoice) {
		t.Fatalf("logs = %s", logs)
	}
}

func TestAddDraftAttachmentFindsTheDraftByMessageID(t *testing.T) {
	e := draftEnv(t, webDraft)
	invoice := writeFile(t, "invoice.pdf", "%PDF")
	_, out, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{MessageID: " m-7 ", Attachments: []string{invoice}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "find:m-7,draftraw:d-7,update:d-7" || out.DraftID != "d-7" {
		t.Fatalf("calls = %v out = %+v", e.work.calls, out)
	}
	// draft_id wins when both are given: no lookup.
	e = draftEnv(t, webDraft)
	if _, _, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", MessageID: "m-other", Attachments: []string{invoice}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.work.calls, ",") != "draftraw:d-7,update:d-7" {
		t.Fatalf("calls = %v", e.work.calls)
	}
	// A message that is not a draft, or a message id a later save replaced.
	e = draftEnv(t, webDraft)
	_, _, err = e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{MessageID: "m-1", Attachments: []string{invoice}})
	if err == nil || !strings.Contains(err.Error(), "message m-1 is not a draft") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(e.work.calls, ",") != "find:m-1" {
		t.Fatalf("calls = %v, want no update", e.work.calls)
	}
	// An unknown draft id.
	_, _, err = e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-gone", Attachments: []string{invoice}})
	if err == nil || !strings.Contains(err.Error(), "not found: d-gone") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddDraftAttachmentKeepsEarlierAttachments(t *testing.T) {
	// First call wraps the web draft, second call extends the result.
	e := draftEnv(t, webDraft)
	first := writeFile(t, "invoice.pdf", "%PDF first")
	if _, _, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", Attachments: []string{first}}); err != nil {
		t.Fatal(err)
	}
	afterFirst := e.work.updateRaw
	e.work.drafts["d-7"].Raw = afterFirst
	second, third := writeFile(t, "hours.xlsx", "sheet"), writeFile(t, "photo.JPG", "jpeg")
	if _, _, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", Attachments: []string{second, third}}); err != nil {
		t.Fatal(err)
	}
	// The second update only inserts: what was there is a prefix of it.
	closing := bytes.LastIndex(afterFirst, []byte("\r\n--"))
	if !bytes.HasPrefix(e.work.updateRaw, afterFirst[:closing]) || !bytes.HasSuffix(e.work.updateRaw, afterFirst[closing:]) {
		t.Fatalf("second update rewrote the message:\n%s", e.work.updateRaw)
	}
	_, parts := splitMixed(t, e.work.updateRaw)
	if len(parts) != 4 || string(parts[0].body) != webDraftBody {
		t.Fatalf("parts = %d", len(parts))
	}
	got := ""
	for _, p := range parts[1:] {
		got += p.name + "=" + p.decoded(t) + ";"
	}
	if got != "invoice.pdf=%PDF first;hours.xlsx=sheet;photo.JPG=jpeg;" {
		t.Fatalf("attachments = %q", got)
	}
	if !strings.HasPrefix(parts[3].ctype, "image/jpeg") {
		t.Fatalf("type of photo.JPG = %q, the extension is matched without case", parts[3].ctype)
	}
}

func TestAddDraftAttachmentSizeLimitCountsTheDraft(t *testing.T) {
	// The file fits alone; the draft that exists pushes the sum over.
	lowerDraftLimit(t, int64(len(webDraft))+100)
	e := draftEnv(t, webDraft)
	invoice := writeFile(t, "invoice.pdf", strings.Repeat("a", 150))
	_, _, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", Attachments: []string{invoice}})
	if err == nil || !strings.Contains(err.Error(), "25 MB limit") || !strings.Contains(err.Error(), invoice) {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(e.work.calls, ",") != "draftraw:d-7" {
		t.Fatalf("calls = %v, want no update", e.work.calls)
	}
	// A file above the limit on its own is refused before any Gmail call.
	e = draftEnv(t, webDraft)
	huge := writeFile(t, "huge.pdf", strings.Repeat("a", len(webDraft)+200))
	_, _, err = e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7", Attachments: []string{huge}})
	if err == nil || !strings.Contains(err.Error(), huge) || len(e.work.calls) != 0 {
		t.Fatalf("err = %v calls = %v", err, e.work.calls)
	}
}

func TestAddDraftAttachmentValidationAndAccount(t *testing.T) {
	e := draftEnv(t, webDraft)
	invoice := writeFile(t, "invoice.pdf", "%PDF")
	if _, _, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{Attachments: []string{invoice}}); err == nil || !strings.Contains(err.Error(), "draft_id or message_id is required") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{DraftID: "d-7"}); err == nil || !strings.Contains(err.Error(), "attachments is required") {
		t.Fatalf("err = %v", err)
	}
	if len(e.asked) != 0 {
		t.Fatalf("asked = %v, want no service for an invalid call", e.asked)
	}
	e.personal.drafts = map[string]*gmail.DraftRaw{"d-p": {DraftID: "d-p", MessageID: "m-p", Raw: []byte(webDraft)}}
	_, out, err := e.h.addDraftAttachment(context.Background(), nil, AddDraftAttachmentInput{Account: "personal", DraftID: "d-p", Attachments: []string{invoice}})
	if err != nil || out.Account != "personal" || out.Email != "p@gmail.com" || len(e.work.calls) != 0 {
		t.Fatalf("out = %+v err %v work calls %v", out, err, e.work.calls)
	}
}

func TestEveryAllowedTypeHasAContentType(t *testing.T) {
	for _, ext := range allowedAttachmentTypes {
		if attachmentContentTypes[ext] == "" {
			t.Errorf("type %s has no content type", ext)
		}
	}
	if len(attachmentContentTypes) != len(allowedAttachmentTypes) {
		t.Fatalf("content types = %d, allowed types = %d", len(attachmentContentTypes), len(allowedAttachmentTypes))
	}
}

// TestDraftAttachmentSchemas reads the two schemas the way Claude Code
// lists them: attachments is optional on create_draft and required on
// add_draft_attachment, where neither id is required by itself.
func TestDraftAttachmentSchemas(t *testing.T) {
	e := newEnv(t)
	type schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	got := map[string]schema{}
	for _, tool := range listTools(t, e) {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var s schema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		got[tool.Name] = s
	}
	create, add := got["create_draft"], got["add_draft_attachment"]
	if !strings.Contains(string(create.Properties["attachments"]), `"array"`) || strings.Join(create.Required, ",") != "body_text" {
		t.Fatalf("create_draft schema = %+v", create)
	}
	if !strings.Contains(string(add.Properties["attachments"]), `"array"`) || strings.Join(add.Required, ",") != "attachments" {
		t.Fatalf("add_draft_attachment schema = %+v", add)
	}
	for _, name := range []string{"account", "draft_id", "message_id"} {
		if _, ok := add.Properties[name]; !ok {
			t.Errorf("add_draft_attachment has no %s property", name)
		}
	}
}
