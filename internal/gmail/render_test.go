package gmail

import (
	"encoding/json"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
)

func loadFixture(t *testing.T, name string) *gmailapi.Message {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var m gmailapi.Message
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &m
}

func identityNames(ids []string) []string { return ids }

func TestRenderPlain(t *testing.T) {
	msg := renderMessage(loadFixture(t, "plain.json"), identityNames, 5000)
	if msg.MessageID != "m-plain" || msg.ThreadID != "t-1" {
		t.Fatalf("ids: %+v", msg)
	}
	if !msg.Date.Equal(time.UnixMilli(1700000000000)) {
		t.Fatalf("date = %v", msg.Date)
	}
	if msg.From != "Ann Example <ann@example.com>" || msg.ReplyTo != "list@example.com" || msg.Subject != "Plain subject" {
		t.Fatalf("headers: %+v", msg)
	}
	// net/mail.Address.String() always wraps the addr-spec in angle brackets and
	// quotes a printable-ASCII name, even when quoting isn't strictly required.
	if strings.Join(msg.To, "|") != `<control@example.com>|"Bob" <bob@example.com>` || strings.Join(msg.Cc, "|") != "<carol@example.com>" {
		t.Fatalf("to=%v cc=%v", msg.To, msg.Cc)
	}
	if msg.BodyText != "Hello from plain text.\n" || msg.BodySource != "text/plain" || msg.BodyTruncated {
		t.Fatalf("body: %+v", msg)
	}
	if strings.Join(msg.Labels, ",") != "INBOX,UNREAD" || len(msg.Attachments) != 0 {
		t.Fatalf("labels=%v attachments=%v", msg.Labels, msg.Attachments)
	}
}

func TestRenderHTMLOnlyConvertsToText(t *testing.T) {
	msg := renderMessage(loadFixture(t, "html_only.json"), identityNames, 5000)
	if msg.BodySource != "text/html" {
		t.Fatalf("source = %q", msg.BodySource)
	}
	if msg.BodyText != "Hello bold\n\nLine two & more" {
		t.Fatalf("body = %q", msg.BodyText)
	}
}

func TestRenderAlternativePrefersPlain(t *testing.T) {
	msg := renderMessage(loadFixture(t, "alternative.json"), identityNames, 5000)
	if msg.BodyText != "Alt plain body\n" || msg.BodySource != "text/plain" {
		t.Fatalf("body: %+v", msg)
	}
}

func TestRenderMixedListsAttachmentAndSkipsIt(t *testing.T) {
	msg := renderMessage(loadFixture(t, "mixed_attachment.json"), identityNames, 5000)
	if msg.BodyText != "Mixed plain body\n" {
		t.Fatalf("body = %q", msg.BodyText)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments = %+v", msg.Attachments)
	}
	a := msg.Attachments[0]
	if a.Filename != "invoice.pdf" || a.MimeType != "application/pdf" || a.SizeBytes != 12345 || a.AttachmentID != "ANGjdJ8attachment" {
		t.Fatalf("attachment = %+v", a)
	}
	if a.Data != nil {
		t.Fatalf("a part with an attachment id must carry no inline data: %q", a.Data)
	}
}

// TestRenderInlineAttachmentWithoutID covers Gmail answering with the bytes
// in body.data instead of an attachment id, which it does for small parts.
// The part with undecodable data is dropped rather than failing the render.
func TestRenderInlineAttachmentWithoutID(t *testing.T) {
	msg := renderMessage(loadFixture(t, "inline_attachment.json"), identityNames, 5000)
	if msg.BodyText != "Inline body\n" {
		t.Fatalf("body = %q", msg.BodyText)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want only the decodable one", msg.Attachments)
	}
	a := msg.Attachments[0]
	if a.Filename != "tiny.txt" || a.MimeType != "text/plain" || a.SizeBytes != 12 || a.AttachmentID != "" {
		t.Fatalf("attachment = %+v", a)
	}
	if string(a.Data) != "inline bytes" {
		t.Fatalf("inline data = %q", a.Data)
	}
}

func TestRenderEmptyBody(t *testing.T) {
	msg := renderMessage(loadFixture(t, "empty.json"), identityNames, 5000)
	if msg.BodyText != "" || msg.BodySource != "none" || msg.BodyTruncated {
		t.Fatalf("body: %+v", msg)
	}
	if msg.To != nil && len(msg.To) != 0 {
		t.Fatalf("to = %v", msg.To)
	}
}

func TestRenderTruncatesOnRuneBoundary(t *testing.T) {
	m := loadFixture(t, "plain.json")
	m.Payload.Body.Data = "T2zDoSwgw6dhIHZhPyDDvG7Dr2PDtmTDqSDinJMgZW5k" // "Olá, ça va? ünïcödé ✓ end"
	msg := renderMessage(m, identityNames, 5)
	if msg.BodyText != "Olá, " || !msg.BodyTruncated {
		t.Fatalf("body = %q truncated = %v", msg.BodyText, msg.BodyTruncated)
	}
	msg = renderMessage(m, identityNames, 500)
	if msg.BodyText != "Olá, ça va? ünïcödé ✓ end" || msg.BodyTruncated {
		t.Fatalf("body = %q truncated = %v", msg.BodyText, msg.BodyTruncated)
	}
}

func TestRenderMapsLabelNames(t *testing.T) {
	names := func(ids []string) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = "name:" + id
		}
		return out
	}
	msg := renderMessage(loadFixture(t, "plain.json"), names, 10)
	if strings.Join(msg.Labels, ",") != "name:INBOX,name:UNREAD" {
		t.Fatalf("labels = %v", msg.Labels)
	}
}

func TestDecodeBodyAcceptsPaddedAndUnpadded(t *testing.T) {
	for _, in := range []string{"SGVsbG8gZnJvbSBwbGFpbiB0ZXh0Lgo", "SGVsbG8gZnJvbSBwbGFpbiB0ZXh0Lgo="} {
		got, err := decodeBody(in)
		if err != nil || string(got) != "Hello from plain text.\n" {
			t.Fatalf("decodeBody(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := decodeBody("***"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestSplitAddresses(t *testing.T) {
	got := splitAddresses("control@example.com, Bob <bob@example.com>")
	if strings.Join(got, "|") != `<control@example.com>|"Bob" <bob@example.com>` {
		t.Fatalf("got %v", got)
	}
	if got := splitAddresses(""); len(got) != 0 {
		t.Fatalf("empty -> %v", got)
	}
	got = splitAddresses("broken <<x>, y@example.com")
	if len(got) != 2 {
		t.Fatalf("fallback split failed: %v", got)
	}
	got = splitAddresses(`"Doe, John" <doe@example.com>, Bob <bob@example.com>`)
	if len(got) != 2 {
		t.Fatalf("quoted display name with comma: %v", got)
	}
	for i, addr := range got {
		parsed, err := mail.ParseAddress(addr)
		if err != nil {
			t.Fatalf("entry %d (%q) does not round-trip through mail.ParseAddress: %v", i, addr, err)
		}
		if i == 0 && parsed.Name != "Doe, John" {
			t.Fatalf("entry 0 name = %q, want %q", parsed.Name, "Doe, John")
		}
	}
}

func TestSummarizeThread(t *testing.T) {
	first := loadFixture(t, "plain.json")
	last := loadFixture(t, "html_only.json")
	last.LabelIds = []string{"INBOX", "Label_7"}
	th := &gmailapi.Thread{Id: "t-1", Snippet: "thread snippet", Messages: []*gmailapi.Message{first, last}}
	s := summarizeThread(th, identityNames)
	if s.ThreadID != "t-1" || s.MessageCount != 2 || s.From != "ann@example.com" || s.Subject != "HTML subject" {
		t.Fatalf("summary = %+v", s)
	}
	if !s.LastMessageAt.Equal(time.UnixMilli(1700000001000)) || s.Snippet != "thread snippet" || !s.Unread {
		t.Fatalf("summary = %+v", s)
	}
	if strings.Join(s.Labels, ",") != "INBOX,UNREAD,Label_7" {
		t.Fatalf("labels = %v", s.Labels)
	}
	if s := summarizeThread(&gmailapi.Thread{Id: "t-x"}, identityNames); s.MessageCount != 0 || s.ThreadID != "t-x" {
		t.Fatalf("empty thread summary = %+v", s)
	}
}

func TestNotFoundError(t *testing.T) {
	if (&NotFoundError{ID: "abc"}).Error() != "not found: abc" {
		t.Fatal((&NotFoundError{ID: "abc"}).Error())
	}
}
