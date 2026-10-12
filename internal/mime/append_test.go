package mime

import (
	"bytes"
	"encoding/base64"
	"io"
	stdmime "mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
)

// webDraft is a reply the way Gmail's web compose stores it: plain and
// HTML alternatives, the HTML quoted-printable, the quoted thread inside.
const webDraftBody = "--000000000000a1b2c3\r\n" +
	"Content-Type: text/plain; charset=\"UTF-8\"\r\n" +
	"\r\n" +
	"Hi Ann,\r\n\r\nThe invoice is attached.\r\n\r\nOn Mon, Ann wrote:\r\n> Please send it.\r\n" +
	"\r\n--000000000000a1b2c3\r\n" +
	"Content-Type: text/html; charset=\"UTF-8\"\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"<div dir=3D\"ltr\">Hi Ann,<br><br>The invoice is attached.</div><br><div class=\r\n" +
	"=3D\"gmail_quote\"><blockquote>Please send it.</blockquote></div>\r\n" +
	"\r\n--000000000000a1b2c3--\r\n"

const webDraftHead = "MIME-Version: 1.0\r\n" +
	"Date: Mon, 12 Oct 2026 09:00:00 +0200\r\n" +
	"References: <root@mail.example.com>\r\n" +
	" <orig@mail.example.com>\r\n" +
	"In-Reply-To: <orig@mail.example.com>\r\n" +
	"Message-ID: <draft@mail.example.com>\r\n" +
	"Subject: Re: =?UTF-8?Q?Rechnung_M=C3=A4rz?=\r\n" +
	"From: Control <control@example.com>\r\n" +
	"To: Ann <ann@example.com>\r\n" +
	"Cc: bob@example.com\r\n" +
	"Bcc: dan@example.com\r\n" +
	"Content-Type: multipart/alternative; boundary=\"000000000000a1b2c3\"\r\n"

const webDraft = webDraftHead + "\r\n" + webDraftBody

// entity is one part of a multipart/mixed message: its header and its body
// bytes exactly as they travel, nothing decoded.
type entity struct {
	header textproto.MIMEHeader
	body   []byte
}

// splitMixed reads a multipart/mixed message without decoding any part.
// multipart.Reader decodes quoted-printable by itself only when the part
// says so, and no part of these messages does at the top level.
func splitMixed(t *testing.T, raw []byte) (mail.Header, []entity) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	mt, params, err := stdmime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []entity
	for {
		p, err := mr.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRawPart: %v\n%s", err, raw)
		}
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts = append(parts, entity{header: p.Header, body: body})
	}
	return msg.Header, parts
}

// attachmentOf checks that a part is a base64 attachment and returns its
// filename and decoded bytes.
func attachmentOf(t *testing.T, e entity) (string, string, []byte) {
	t.Helper()
	ct, _, err := stdmime.ParseMediaType(e.header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type = %q (%v)", e.header.Get("Content-Type"), err)
	}
	disp, params, err := stdmime.ParseMediaType(e.header.Get("Content-Disposition"))
	if err != nil || disp != "attachment" {
		t.Fatalf("Content-Disposition = %q (%v)", e.header.Get("Content-Disposition"), err)
	}
	if e.header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("encoding = %q", e.header.Get("Content-Transfer-Encoding"))
	}
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(e.body)))
	if err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	return params["filename"], ct, data
}

func pdf(name, data string) Attachment {
	return Attachment{Filename: name, ContentType: "application/pdf", Data: []byte(data)}
}

func TestAddAttachmentsKeepsAWebDraftByteForByte(t *testing.T) {
	out, err := AddAttachments([]byte(webDraft), []Attachment{pdf("invoice.pdf", "%PDF-1.4 fake")})
	if err != nil {
		t.Fatal(err)
	}
	// Every header field that is not about the content stays on the
	// message, in order, folded lines included.
	kept := strings.TrimSuffix(webDraftHead, "Content-Type: multipart/alternative; boundary=\"000000000000a1b2c3\"\r\n")
	if !bytes.HasPrefix(out, []byte(kept+"Content-Type: multipart/mixed; boundary=")) {
		t.Fatalf("header changed:\n%s", out)
	}
	if n := bytes.Count(out, []byte("MIME-Version:")); n != 1 {
		t.Fatalf("MIME-Version appears %d times", n)
	}
	header, parts := splitMixed(t, out)
	for name, want := range map[string]string{
		"To": "Ann <ann@example.com>", "Cc": "bob@example.com", "Bcc": "dan@example.com",
		"In-Reply-To": "<orig@mail.example.com>", "References": "<root@mail.example.com> <orig@mail.example.com>",
		"Subject": "Re: =?UTF-8?Q?Rechnung_M=C3=A4rz?=", "Message-Id": "<draft@mail.example.com>",
	} {
		if got := header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	// The first part is the old message: its Content-Type moved down and
	// its body, both alternatives, is the same bytes.
	if got := parts[0].header.Get("Content-Type"); got != `multipart/alternative; boundary="000000000000a1b2c3"` {
		t.Fatalf("first part Content-Type = %q", got)
	}
	if string(parts[0].body) != webDraftBody {
		t.Fatalf("body changed:\n%q\nwant\n%q", parts[0].body, webDraftBody)
	}
	name, ct, data := attachmentOf(t, parts[1])
	if name != "invoice.pdf" || ct != "application/pdf" || string(data) != "%PDF-1.4 fake" {
		t.Fatalf("attachment = %q %q %q", name, ct, data)
	}
}

func TestAddAttachmentsExtendsAMixedMessageInPlace(t *testing.T) {
	// A draft that already carries one attachment next to its alternatives.
	old := "--outer\r\n" +
		"Content-Type: multipart/alternative; boundary=\"000000000000a1b2c3\"\r\n" +
		"\r\n" + webDraftBody +
		"--outer\r\n" +
		"Content-Type: application/pdf; name=\"first.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"first.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"Zmlyc3Q=\r\n"
	raw := "To: ann@example.com\r\nSubject: S\r\nContent-Type: multipart/mixed;\r\n boundary=\"outer\"\r\n\r\n" + old + "--outer--\r\n"
	out, err := AddAttachments([]byte(raw), []Attachment{pdf("second.pdf", "second"), {Filename: "notes.txt", ContentType: "text/plain", Data: []byte("notes")}})
	if err != nil {
		t.Fatal(err)
	}
	// Everything before the closing boundary is untouched and the closing
	// boundary is still the end.
	untouched := strings.TrimSuffix(raw, "\r\n--outer--\r\n")
	if !bytes.HasPrefix(out, []byte(untouched+"\r\n--outer\r\n")) || !bytes.HasSuffix(out, []byte("\r\n--outer--\r\n")) {
		t.Fatalf("message was rewritten:\n%s", out)
	}
	if bytes.Count(out, []byte("multipart/mixed")) != 1 {
		t.Fatalf("a second multipart/mixed was added:\n%s", out)
	}
	_, parts := splitMixed(t, out)
	if len(parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(parts))
	}
	var names []string
	for _, p := range parts[1:] {
		name, _, _ := attachmentOf(t, p)
		names = append(names, name)
	}
	if strings.Join(names, ",") != "first.pdf,second.pdf,notes.txt" {
		t.Fatalf("attachments = %v", names)
	}
	if _, _, data := attachmentOf(t, parts[1]); string(data) != "first" {
		t.Fatalf("old attachment = %q", data)
	}
	if _, _, data := attachmentOf(t, parts[3]); string(data) != "notes" {
		t.Fatalf("new attachment = %q", data)
	}
}

func TestAddAttachmentsWrapsADraftBuiltByBuild(t *testing.T) {
	m := baseMessage()
	m.BodyText = "Grüße\n"
	m.InReplyTo = "<orig@mail.example.com>"
	m.References = []string{"<orig@mail.example.com>"}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	out, err := AddAttachments(raw, []Attachment{pdf("invoice.pdf", "x")})
	if err != nil {
		t.Fatal(err)
	}
	header, parts := splitMixed(t, out)
	if header.Get("In-Reply-To") != "<orig@mail.example.com>" || header.Get("Subject") != "Fwd: Hello" || header.Get("Content-Transfer-Encoding") != "" {
		t.Fatalf("headers = %+v", header)
	}
	if len(parts) != 2 || parts[0].header.Get("Content-Type") != "text/plain; charset=utf-8" || parts[0].header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("parts = %+v", parts)
	}
	text, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(parts[0].body)))
	if err != nil || string(text) != "Grüße\n" {
		t.Fatalf("text = %q (%v)", text, err)
	}
}

func TestAddAttachmentsUTF8Filename(t *testing.T) {
	out, err := AddAttachments([]byte(webDraft), []Attachment{pdf("Rechnung März.pdf", "x")})
	if err != nil {
		t.Fatal(err)
	}
	_, parts := splitMixed(t, out)
	name, _, _ := attachmentOf(t, parts[1])
	if name != "Rechnung März.pdf" {
		t.Fatalf("filename = %q", name)
	}
	if !bytes.Contains(out, []byte("filename*=utf-8''Rechnung%20M%C3%A4rz.pdf")) {
		t.Fatalf("expected the RFC 2231 filename in\n%s", out)
	}
	for _, name := range []string{"Content-Type", "Content-Disposition"} {
		for _, r := range parts[1].header.Get(name) {
			if r > 0x7f {
				t.Fatalf("non-ASCII byte in attachment %s: %q", name, parts[1].header.Get(name))
			}
		}
	}
}

// A multipart/mixed message whose closing boundary is missing cannot be
// extended in place. It is wrapped whole, so nothing is lost.
func TestAddAttachmentsWrapsWhenTheClosingBoundaryIsMissing(t *testing.T) {
	body := "--outer\r\nContent-Type: text/plain\r\n\r\nhello\r\n"
	raw := "To: ann@example.com\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n" + body
	out, err := AddAttachments([]byte(raw), []Attachment{pdf("invoice.pdf", "x")})
	if err != nil {
		t.Fatal(err)
	}
	_, parts := splitMixed(t, out)
	if len(parts) != 2 || parts[0].header.Get("Content-Type") != "multipart/mixed; boundary=outer" || string(parts[0].body) != body {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestAddAttachmentsBareLineFeeds(t *testing.T) {
	raw := "To: ann@example.com\nSubject: S\nContent-Type: text/plain\n\nline one\nline two\n"
	out, err := AddAttachments([]byte(raw), []Attachment{pdf("invoice.pdf", "x")})
	if err != nil {
		t.Fatal(err)
	}
	header, parts := splitMixed(t, out)
	if header.Get("Subject") != "S" || len(parts) != 2 || string(parts[0].body) != "line one\nline two\n" || parts[0].header.Get("Content-Type") != "text/plain" {
		t.Fatalf("header = %+v parts = %+v", header, parts)
	}
}

func TestAddAttachmentsMessageWithoutABody(t *testing.T) {
	out, err := AddAttachments([]byte("To: ann@example.com\r\nSubject: S"), []Attachment{pdf("invoice.pdf", "x")})
	if err != nil {
		t.Fatal(err)
	}
	header, parts := splitMixed(t, out)
	if header.Get("Subject") != "S" || header.Get("Mime-Version") != "1.0" || len(parts) != 2 || len(parts[0].body) != 0 {
		t.Fatalf("header = %+v parts = %+v", header, parts)
	}
}

// An attached .eml travels verbatim. When it holds the draft's own boundary
// text, extending in place would cut the message there, so it is wrapped.
func TestAddAttachmentsEmlThatHoldsTheBoundary(t *testing.T) {
	raw := "To: ann@example.com\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n--outer\r\nContent-Type: text/plain\r\n\r\nhello\r\n--outer--\r\n"
	eml := []byte("Subject: inner\r\n\r\ntext\r\n--outer--\r\n")
	out, err := AddAttachments([]byte(raw), []Attachment{{Filename: "inner.eml", ContentType: "message/rfc822", Data: eml}})
	if err != nil {
		t.Fatal(err)
	}
	_, parts := splitMixed(t, out)
	if len(parts) != 2 || parts[0].header.Get("Content-Type") != "multipart/mixed; boundary=outer" {
		t.Fatalf("parts = %+v", parts)
	}
	// The last CRLF of the eml reads as part of the boundary that follows.
	if parts[1].header.Get("Content-Transfer-Encoding") != "8bit" || !bytes.Equal(parts[1].body, bytes.TrimSuffix(eml, []byte("\r\n"))) {
		t.Fatalf("eml part = %+v %q", parts[1].header, parts[1].body)
	}
}

func TestAddAttachmentsValidation(t *testing.T) {
	if _, err := AddAttachments([]byte(webDraft), nil); err == nil {
		t.Fatal("no attachments accepted")
	}
	if _, err := AddAttachments([]byte(webDraft), []Attachment{{Filename: "a.pdf"}}); err == nil || !strings.Contains(err.Error(), "a.pdf") {
		t.Fatalf("err = %v, want error naming the attachment without data", err)
	}
	if _, err := AddAttachments([]byte(webDraft), []Attachment{{Data: []byte("x")}}); err == nil {
		t.Fatal("attachment without a filename accepted")
	}
}
