package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// attachmentEnv is newEnv with a throwaway attachment directory and one
// message on the work account.
func attachmentEnv(t *testing.T, atts ...gmail.Attachment) (*env, string) {
	t.Helper()
	e := newEnv(t)
	dir := t.TempDir()
	e.h.d.AttachmentDir = dir
	e.work.message = &gmail.Message{MessageID: "m-1", ThreadID: "t-1", BodySource: "none", Attachments: atts}
	e.work.attachments = map[string][]byte{
		"att-pdf": []byte("%PDF-1.4 fake"),
		"att-png": []byte("png bytes"),
		"att-zip": []byte("zip bytes"),
	}
	return e, dir
}

func mustInside(t *testing.T, dir, path string) {
	t.Helper()
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("path %q is outside %q (rel %q, err %v)", path, dir, rel, err)
	}
}

func TestGetAttachmentSavesTheNamedFile(t *testing.T) {
	e, dir := attachmentEnv(t,
		gmail.Attachment{Filename: "invoice.pdf", MimeType: "application/pdf", SizeBytes: 13, AttachmentID: "att-pdf"},
		gmail.Attachment{Filename: "photo.png", MimeType: "image/png", SizeBytes: 9, AttachmentID: "att-png"},
	)
	_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1", Filename: "invoice.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Account != "work" || out.Email != "control@example.com" || out.MessageID != "m-1" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Files) != 1 || len(out.Skipped) != 0 {
		t.Fatalf("files = %+v skipped = %+v", out.Files, out.Skipped)
	}
	f := out.Files[0]
	if f.Filename != "invoice.pdf" || f.MimeType != "application/pdf" || f.SizeBytes != 13 {
		t.Fatalf("file = %+v", f)
	}
	want := filepath.Join(dir, "work", "m-1", "invoice.pdf")
	if f.Path != want {
		t.Fatalf("path = %q, want %q", f.Path, want)
	}
	data, err := os.ReadFile(f.Path)
	if err != nil || string(data) != "%PDF-1.4 fake" {
		t.Fatalf("file content = %q err %v", data, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(f.Path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v err %v", info.Mode().Perm(), err)
		}
		dirInfo, err := os.Stat(filepath.Dir(f.Path))
		if err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v err %v", dirInfo.Mode().Perm(), err)
		}
	}
	if strings.Contains(strings.Join(e.work.calls, ","), "att:att-png") {
		t.Fatalf("calls = %v: the other attachment must not be downloaded", e.work.calls)
	}
}

func TestGetAttachmentWithoutFilenameSavesEveryAllowedFile(t *testing.T) {
	e, _ := attachmentEnv(t,
		gmail.Attachment{Filename: "invoice.pdf", MimeType: "application/pdf", SizeBytes: 13, AttachmentID: "att-pdf"},
		gmail.Attachment{Filename: "bundle.zip", MimeType: "application/zip", SizeBytes: 9, AttachmentID: "att-zip"},
		gmail.Attachment{Filename: "photo.PNG", MimeType: "image/png", SizeBytes: 9, AttachmentID: "att-png"},
	)
	_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 2 || out.Files[0].Filename != "invoice.pdf" || out.Files[1].Filename != "photo.PNG" {
		t.Fatalf("files = %+v", out.Files)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Filename != "bundle.zip" || !strings.Contains(out.Skipped[0].Reason, "zip") {
		t.Fatalf("skipped = %+v", out.Skipped)
	}
	if strings.Contains(strings.Join(e.work.calls, ","), "att:att-zip") {
		t.Fatalf("calls = %v: a skipped file must not be downloaded", e.work.calls)
	}
}

func TestGetAttachmentRejectsATypeOutsideTheList(t *testing.T) {
	e, dir := attachmentEnv(t,
		gmail.Attachment{Filename: "bundle.zip", MimeType: "application/zip", SizeBytes: 9, AttachmentID: "att-zip"},
	)
	for _, in := range []GetAttachmentInput{{MessageID: "m-1", Filename: "bundle.zip"}, {MessageID: "m-1"}} {
		_, _, err := e.h.getAttachment(context.Background(), nil, in)
		if err == nil || !strings.Contains(err.Error(), "bundle.zip") || !strings.Contains(err.Error(), "pdf") {
			t.Fatalf("in %+v: err = %v, want the file name and the allowed types", in, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("nothing may be written for a rejected type: %v", entries)
	}
}

// A sender controls the filename. Whatever it holds, the file lands inside
// the message's own directory.
func TestGetAttachmentFilenameCannotLeaveTheDirectory(t *testing.T) {
	cases := map[string]string{
		"../../evil.pdf":                  "evil.pdf",
		"/etc/passwd.txt":                 "passwd.txt",
		`..\..\windows\evil.pdf`:          "evil.pdf",
		".hidden.pdf":                     "hidden.pdf",
		"a\x00b\nc.pdf":                   "abc.pdf",
		"sub/dir/report.pdf":              "report.pdf",
		"  spaced name .pdf":              "spaced name .pdf",
		strings.Repeat("x", 400) + ".pdf": strings.Repeat("x", 150) + ".pdf",
	}
	for name, wantBase := range cases {
		e, dir := attachmentEnv(t, gmail.Attachment{Filename: name, MimeType: "application/pdf", SizeBytes: 13, AttachmentID: "att-pdf"})
		_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if len(out.Files) != 1 {
			t.Fatalf("%q: files = %+v skipped = %+v", name, out.Files, out.Skipped)
		}
		got := out.Files[0]
		mustInside(t, filepath.Join(dir, "work", "m-1"), got.Path)
		if filepath.Base(got.Path) != wantBase {
			t.Errorf("%q: saved as %q, want %q", name, filepath.Base(got.Path), wantBase)
		}
		if got.Filename != name {
			t.Errorf("%q: filename in the result = %q, want the original", name, got.Filename)
		}
		if _, err := os.Stat(got.Path); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestGetAttachmentRejectsAMessageIDThatIsAPath(t *testing.T) {
	for _, id := range []string{"../m-1", "a/b", "..", "m 1", `a\b`} {
		e, dir := attachmentEnv(t, gmail.Attachment{Filename: "invoice.pdf", SizeBytes: 13, AttachmentID: "att-pdf"})
		_, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: id})
		if err == nil || !strings.Contains(err.Error(), "message_id") {
			t.Fatalf("%q: err = %v", id, err)
		}
		if len(e.work.calls) != 0 {
			t.Fatalf("%q: calls = %v, want none", id, e.work.calls)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatalf("%q: wrote %v", id, entries)
		}
	}
	e, _ := attachmentEnv(t)
	if _, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: " "}); err == nil || !strings.Contains(err.Error(), "message_id is required") {
		t.Fatalf("blank id: err = %v", err)
	}
}

func TestGetAttachmentUnknownFilenameListsTheNames(t *testing.T) {
	e, _ := attachmentEnv(t,
		gmail.Attachment{Filename: "invoice.pdf", SizeBytes: 13, AttachmentID: "att-pdf"},
		gmail.Attachment{Filename: "photo.png", SizeBytes: 9, AttachmentID: "att-png"},
	)
	_, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1", Filename: "missing.pdf"})
	if err == nil || !strings.Contains(err.Error(), "missing.pdf") || !strings.Contains(err.Error(), "invoice.pdf") || !strings.Contains(err.Error(), "photo.png") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetAttachmentFilenameMatchIgnoresCase(t *testing.T) {
	e, _ := attachmentEnv(t, gmail.Attachment{Filename: "Invoice.PDF", SizeBytes: 13, AttachmentID: "att-pdf"})
	_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1", Filename: " invoice.pdf "})
	if err != nil || len(out.Files) != 1 || filepath.Base(out.Files[0].Path) != "Invoice.PDF" {
		t.Fatalf("out = %+v err %v", out, err)
	}
}

func TestGetAttachmentMessageWithoutAttachments(t *testing.T) {
	e, _ := attachmentEnv(t)
	_, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err == nil || !strings.Contains(err.Error(), "no attachments") {
		t.Fatalf("err = %v", err)
	}
}

// Gmail returns small parts inline: bytes in the message, no attachment id.
func TestGetAttachmentUsesInlineBytes(t *testing.T) {
	e, _ := attachmentEnv(t, gmail.Attachment{Filename: "note.txt", MimeType: "text/plain", SizeBytes: 5, Data: []byte("hello")})
	_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err != nil || len(out.Files) != 1 {
		t.Fatalf("out = %+v err %v", out, err)
	}
	data, _ := os.ReadFile(out.Files[0].Path)
	if string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	for _, c := range e.work.calls {
		if strings.HasPrefix(c, "att:") {
			t.Fatalf("calls = %v, want no download", e.work.calls)
		}
	}
}

func TestGetAttachmentWithoutIDOrBytesFails(t *testing.T) {
	e, _ := attachmentEnv(t, gmail.Attachment{Filename: "note.txt", SizeBytes: 5})
	_, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err == nil || !strings.Contains(err.Error(), "note.txt") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetAttachmentSameNameGetsANumber(t *testing.T) {
	e, _ := attachmentEnv(t,
		gmail.Attachment{Filename: "scan.pdf", SizeBytes: 13, AttachmentID: "att-pdf"},
		gmail.Attachment{Filename: "dir/scan.pdf", SizeBytes: 9, AttachmentID: "att-png"},
		gmail.Attachment{Filename: "SCAN.pdf", SizeBytes: 9, AttachmentID: "att-zip"},
	)
	_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err != nil || len(out.Files) != 3 {
		t.Fatalf("out = %+v err %v", out, err)
	}
	var names []string
	for _, f := range out.Files {
		names = append(names, filepath.Base(f.Path))
	}
	// The third name differs only in case, which is the same file on the
	// default macOS file system.
	if strings.Join(names, ",") != "scan.pdf,scan-2.pdf,SCAN-3.pdf" {
		t.Fatalf("names = %v", names)
	}
	first, _ := os.ReadFile(out.Files[0].Path)
	second, _ := os.ReadFile(out.Files[1].Path)
	if string(first) != "%PDF-1.4 fake" || string(second) != "png bytes" {
		t.Fatalf("content = %q, %q", first, second)
	}
}

func TestGetAttachmentSecondCallOverwrites(t *testing.T) {
	e, _ := attachmentEnv(t, gmail.Attachment{Filename: "invoice.pdf", SizeBytes: 13, AttachmentID: "att-pdf"})
	in := GetAttachmentInput{MessageID: "m-1", Filename: "invoice.pdf"}
	_, first, err := e.h.getAttachment(context.Background(), nil, in)
	if err != nil {
		t.Fatal(err)
	}
	e.work.attachments["att-pdf"] = []byte("second")
	_, second, err := e.h.getAttachment(context.Background(), nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Files[0].Path != second.Files[0].Path {
		t.Fatalf("paths differ: %q, %q", first.Files[0].Path, second.Files[0].Path)
	}
	data, _ := os.ReadFile(second.Files[0].Path)
	if string(data) != "second" {
		t.Fatalf("content = %q", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(second.Files[0].Path))
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want 1 (no temp file left)", len(entries))
	}
}

func TestGetAttachmentSizeLimit(t *testing.T) {
	old := maxForwardBytes
	maxForwardBytes = 10
	t.Cleanup(func() { maxForwardBytes = old })

	// Announced size above the limit: refused before any download.
	e, dir := attachmentEnv(t, gmail.Attachment{Filename: "invoice.pdf", SizeBytes: 11, AttachmentID: "att-pdf"})
	_, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range e.work.calls {
		if strings.HasPrefix(c, "att:") {
			t.Fatalf("calls = %v, want no download", e.work.calls)
		}
	}

	// Announced size below the limit, real size above it.
	e, dir = attachmentEnv(t, gmail.Attachment{Filename: "invoice.pdf", SizeBytes: 5, AttachmentID: "att-pdf"})
	_, _, err = e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "m-1", "invoice.pdf")); err == nil {
		t.Fatal("a file above the limit must not be written")
	}
}

func TestGetAttachmentUsesTheNamedAccount(t *testing.T) {
	e, dir := attachmentEnv(t)
	e.personal.message = &gmail.Message{MessageID: "m-1", Attachments: []gmail.Attachment{{Filename: "note.txt", SizeBytes: 5, Data: []byte("hello")}}}
	_, out, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{Account: "personal", MessageID: "m-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Account != "personal" || out.Files[0].Path != filepath.Join(dir, "personal", "m-1", "note.txt") {
		t.Fatalf("out = %+v", out)
	}
}

func TestGetAttachmentWithoutADirectoryFails(t *testing.T) {
	e, _ := attachmentEnv(t, gmail.Attachment{Filename: "invoice.pdf", SizeBytes: 13, AttachmentID: "att-pdf"})
	e.h.d.AttachmentDir = ""
	_, _, err := e.h.getAttachment(context.Background(), nil, GetAttachmentInput{MessageID: "m-1"})
	if err == nil || !strings.Contains(err.Error(), "attachment directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetAttachmentIsListedReadOnly(t *testing.T) {
	e := newEnv(t)
	for _, tool := range listTools(t, e) {
		if tool.Name != "get_attachment" {
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("annotations = %+v", tool.Annotations)
		}
		if !strings.Contains(tool.Description, "pdf") {
			t.Fatalf("description must name the allowed types: %q", tool.Description)
		}
		return
	}
	t.Fatal("get_attachment is not registered")
}
