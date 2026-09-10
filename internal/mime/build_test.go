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
	"time"
)

func parse(t *testing.T, raw []byte) (*mail.Message, string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return msg, string(body)
}

func TestBuildNewMessage(t *testing.T) {
	raw, err := Build(Message{
		From:     "control@example.com",
		To:       []string{"a@example.com", "Bob <b@example.com>"},
		Cc:       []string{"c@example.com"},
		Bcc:      []string{"d@example.com"},
		Subject:  "Hello",
		BodyText: "Line one\nLine two\n",
		Date:     time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\r\n\r\n") {
		t.Fatal("headers and body must be separated by CRLF CRLF")
	}
	msg, body := parse(t, raw)
	if body != "Line one\nLine two\n" {
		t.Fatalf("body = %q", body)
	}
	h := msg.Header
	if h.Get("From") != "control@example.com" || h.Get("To") != "<a@example.com>, \"Bob\" <b@example.com>" ||
		h.Get("Cc") != "<c@example.com>" || h.Get("Bcc") != "<d@example.com>" || h.Get("Subject") != "Hello" {
		t.Fatalf("headers = %+v", h)
	}
	if h.Get("Date") != "Thu, 03 Sep 2026 10:00:00 +0000" {
		t.Fatalf("Date = %q", h.Get("Date"))
	}
	if h.Get("MIME-Version") != "1.0" || h.Get("Content-Type") != "text/plain; charset=utf-8" ||
		h.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("mime headers = %+v", h)
	}
	if h.Get("In-Reply-To") != "" || h.Get("References") != "" {
		t.Fatal("new message must not carry reply headers")
	}
}

func TestBuildEncodesNonASCIISubjectAndBody(t *testing.T) {
	raw, err := Build(Message{From: "a@x.com", To: []string{"b@x.com"}, Subject: "Olá João — ünïcödé", BodyText: "Café ✓"})
	if err != nil {
		t.Fatal(err)
	}
	msg, body := parse(t, raw)
	rawSubject := msg.Header.Get("Subject")
	if !strings.HasPrefix(rawSubject, "=?utf-8?q?") && !strings.HasPrefix(rawSubject, "=?utf-8?Q?") {
		t.Fatalf("subject not RFC 2047 encoded: %q", rawSubject)
	}
	dec := new(stdmime.WordDecoder)
	decoded, err := dec.DecodeHeader(rawSubject)
	if err != nil || decoded != "Olá João — ünïcödé" {
		t.Fatalf("decoded subject = %q err %v", decoded, err)
	}
	if body != "Café ✓" {
		t.Fatalf("body = %q", body)
	}
}

func TestBuildReplyHeaders(t *testing.T) {
	raw, err := Build(Message{
		From: "a@x.com", To: []string{"b@x.com"}, Subject: "Re: Hi", BodyText: "ok",
		InReplyTo:  "<orig@mail.gmail.com>",
		References: []string{"<root@mail.gmail.com>", "<orig@mail.gmail.com>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := parse(t, raw)
	if msg.Header.Get("In-Reply-To") != "<orig@mail.gmail.com>" {
		t.Fatalf("In-Reply-To = %q", msg.Header.Get("In-Reply-To"))
	}
	if msg.Header.Get("References") != "<root@mail.gmail.com> <orig@mail.gmail.com>" {
		t.Fatalf("References = %q", msg.Header.Get("References"))
	}
}

func TestBuildLongBodyWrapsBase64At76(t *testing.T) {
	raw, err := Build(Message{From: "a@x.com", To: []string{"b@x.com"}, Subject: "s", BodyText: strings.Repeat("x", 500)})
	if err != nil {
		t.Fatal(err)
	}
	_, after, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(strings.TrimSpace(after), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line longer than 76: %d", len(line))
		}
	}
}

func TestBuildValidation(t *testing.T) {
	cases := map[string]Message{
		"no from":            {To: []string{"b@x.com"}, Subject: "s", BodyText: "b"},
		"no recipients":      {From: "a@x.com", Subject: "s", BodyText: "b"},
		"empty subject":      {From: "a@x.com", To: []string{"b@x.com"}, BodyText: "b"},
		"empty body":         {From: "a@x.com", To: []string{"b@x.com"}, Subject: "s"},
		"bad address":        {From: "a@x.com", To: []string{"not an address"}, Subject: "s", BodyText: "b"},
		"bad cc":             {From: "a@x.com", To: []string{"b@x.com"}, Cc: []string{"@@"}, Subject: "s", BodyText: "b"},
		"newline in subject": {From: "a@x.com", To: []string{"b@x.com"}, Subject: "s\nBcc: evil@x.com", BodyText: "b"},
	}
	for name, m := range cases {
		if _, err := Build(m); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// TestBuildAndParseAddressesRejectHeaderInjection proves a To address
// carrying an embedded header line (CR or bare LF) cannot smuggle an
// extra Bcc header into the built message: both Build and the ParseAddresses
// it relies on must reject it.
func TestBuildAndParseAddressesRejectHeaderInjection(t *testing.T) {
	for name, addr := range map[string]string{
		"CRLF": "a@x.com\r\nBcc: evil@x.com",
		"LF":   "a@x.com\nBcc: evil@x.com",
	} {
		if _, err := Build(Message{From: "me@x.com", To: []string{addr}, Subject: "s", BodyText: "b"}); err == nil {
			t.Errorf("%s: Build accepted a To address carrying an embedded header", name)
		}
		if _, err := ParseAddresses([]string{addr}); err == nil {
			t.Errorf("%s: ParseAddresses accepted an address carrying an embedded header", name)
		}
	}
}

func TestParseAddresses(t *testing.T) {
	got, err := ParseAddresses([]string{"Bob <b@example.com>", "c@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != `"Bob" <b@example.com>|<c@example.com>` {
		t.Fatalf("got %v", got)
	}
	if _, err := ParseAddresses([]string{"nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want error naming the bad address", err)
	}
}

// parseMultipart returns the top-level media params and the parts of a
// multipart/mixed message, each with its decoded (base64) body.
type part struct {
	header textproto.MIMEHeader
	body   []byte
}

func parseMultipart(t *testing.T, raw []byte) (*mail.Message, []part) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, raw)
	}
	mt, params, err := stdmime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []part
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		var body []byte
		switch enc := p.Header.Get("Content-Transfer-Encoding"); enc {
		case "base64":
			body, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		case "8bit":
			// message/rfc822 travels verbatim (RFC 2046 5.2.1), so the raw
			// part bytes are the attachment bytes.
			body, err = io.ReadAll(p)
		default:
			t.Fatalf("part encoding = %q", enc)
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts = append(parts, part{header: p.Header, body: body})
	}
	return msg, parts
}

func baseMessage() Message {
	return Message{
		From:     "control@example.com",
		To:       []string{"a@example.com"},
		Subject:  "Fwd: Hello",
		BodyText: "See attached\n",
		Date:     time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC),
	}
}

func TestBuildWithOneAttachment(t *testing.T) {
	m := baseMessage()
	m.Attachments = []Attachment{{Filename: "invoice.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 fake")}}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\r\n\r\n") {
		t.Fatal("headers and body must be separated by CRLF CRLF")
	}
	msg, parts := parseMultipart(t, raw)
	if msg.Header.Get("MIME-Version") != "1.0" || msg.Header.Get("Subject") != "Fwd: Hello" {
		t.Fatalf("headers = %+v", msg.Header)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].header.Get("Content-Type") != "text/plain; charset=utf-8" || string(parts[0].body) != "See attached\n" {
		t.Fatalf("text part = %+v %q", parts[0].header, parts[0].body)
	}
	ct, ctParams, err := stdmime.ParseMediaType(parts[1].header.Get("Content-Type"))
	if err != nil || ct != "application/pdf" || ctParams["name"] != "invoice.pdf" {
		t.Fatalf("attachment Content-Type = %q (%v)", parts[1].header.Get("Content-Type"), err)
	}
	disp, dispParams, err := stdmime.ParseMediaType(parts[1].header.Get("Content-Disposition"))
	if err != nil || disp != "attachment" || dispParams["filename"] != "invoice.pdf" {
		t.Fatalf("attachment Content-Disposition = %q (%v)", parts[1].header.Get("Content-Disposition"), err)
	}
	if string(parts[1].body) != "%PDF-1.4 fake" {
		t.Fatalf("attachment body = %q", parts[1].body)
	}
}

func TestBuildWithSeveralAttachmentsKeepsOrderAndDefaultsType(t *testing.T) {
	m := baseMessage()
	m.Attachments = []Attachment{
		{Filename: "one.txt", ContentType: "text/plain", Data: []byte("1")},
		{Filename: "two.bin", Data: []byte{0, 1, 2, 255}},
		{Filename: "three.csv", ContentType: "text/csv", Data: []byte("a,b\n")},
	}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	_, parts := parseMultipart(t, raw)
	if len(parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(parts))
	}
	names := []string{}
	for _, p := range parts[1:] {
		_, params, _ := stdmime.ParseMediaType(p.header.Get("Content-Disposition"))
		names = append(names, params["filename"])
	}
	if strings.Join(names, ",") != "one.txt,two.bin,three.csv" {
		t.Fatalf("order = %v", names)
	}
	ct, _, _ := stdmime.ParseMediaType(parts[2].header.Get("Content-Type"))
	if ct != "application/octet-stream" {
		t.Fatalf("default content type = %q", ct)
	}
	if !bytes.Equal(parts[2].body, []byte{0, 1, 2, 255}) {
		t.Fatalf("binary body = %v", parts[2].body)
	}
}

func TestBuildAttachmentUTF8FilenameSurvives(t *testing.T) {
	m := baseMessage()
	m.Attachments = []Attachment{{Filename: `Rechnung "März" 2026.pdf`, ContentType: "application/pdf", Data: []byte("x")}}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	_, parts := parseMultipart(t, raw)
	_, ctParams, err := stdmime.ParseMediaType(parts[1].header.Get("Content-Type"))
	if err != nil || ctParams["name"] != `Rechnung "März" 2026.pdf` {
		t.Fatalf("name = %q (%v)", ctParams["name"], err)
	}
	_, dispParams, err := stdmime.ParseMediaType(parts[1].header.Get("Content-Disposition"))
	if err != nil || dispParams["filename"] != `Rechnung "März" 2026.pdf` {
		t.Fatalf("filename = %q (%v)", dispParams["filename"], err)
	}
	// RFC 2231 form must be used for the non-ASCII name. The filename lives
	// only in the attachment part's own headers, so those are the ones that
	// must be free of raw UTF-8.
	for _, name := range []string{"Content-Type", "Content-Disposition"} {
		v := parts[1].header.Get(name)
		if v == "" {
			t.Fatalf("attachment part has no %s header", name)
		}
		for _, r := range v {
			if r > 0x7f {
				t.Fatalf("non-ASCII byte in attachment %s: %q", name, v)
			}
		}
	}
	if !strings.Contains(string(raw), "filename*=utf-8''") {
		t.Fatalf("expected RFC 2231 filename parameter in\n%s", raw)
	}
}

func TestBuildRFC822Attachment(t *testing.T) {
	inner := []byte("From: x@example.com\r\nSubject: inner\r\n\r\nhello\r\n")
	m := baseMessage()
	m.Attachments = []Attachment{{Filename: "inner.eml", ContentType: "message/rfc822", Data: inner}}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	_, parts := parseMultipart(t, raw)
	ct, ctParams, _ := stdmime.ParseMediaType(parts[1].header.Get("Content-Type"))
	if ct != "message/rfc822" || ctParams["name"] != "inner.eml" {
		t.Fatalf("rfc822 Content-Type = %q", parts[1].header.Get("Content-Type"))
	}
	_, dispParams, _ := stdmime.ParseMediaType(parts[1].header.Get("Content-Disposition"))
	if dispParams["filename"] != "inner.eml" {
		t.Fatalf("rfc822 Content-Disposition = %q", parts[1].header.Get("Content-Disposition"))
	}
	// RFC 2046 section 5.2.1 allows only 7bit, 8bit or binary here. Outlook
	// shows a base64 message/rfc822 part as empty.
	if enc := parts[1].header.Get("Content-Transfer-Encoding"); enc != "8bit" {
		t.Fatalf("rfc822 encoding = %q, want 8bit", enc)
	}
	// parseMultipart returns 8bit parts undecoded, so this compares the
	// bytes on the wire with the attachment bytes.
	if !bytes.Equal(parts[1].body, inner) {
		t.Fatalf("rfc822 body = %q, want the inner message verbatim", parts[1].body)
	}
	if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(inner)[:20])) {
		t.Fatal("the inner message must not be base64 encoded anywhere in the output")
	}
}

// TestBuildRFC822AttachmentTerminatesLastLine covers an inner message that
// does not end with CRLF: the part gets one so the boundary starts on its
// own line.
func TestBuildRFC822AttachmentTerminatesLastLine(t *testing.T) {
	inner := []byte("From: x@example.com\r\nSubject: inner\r\n\r\nno trailing newline")
	m := baseMessage()
	m.Attachments = []Attachment{{Filename: "inner.eml", ContentType: "MESSAGE/RFC822", Data: inner}}
	raw, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	_, parts := parseMultipart(t, raw)
	if enc := parts[1].header.Get("Content-Transfer-Encoding"); enc != "8bit" {
		t.Fatalf("encoding = %q, want 8bit for MESSAGE/RFC822 in any letter case", enc)
	}
	if !bytes.Equal(parts[1].body, append(append([]byte{}, inner...), "\r\n"...)) {
		t.Fatalf("body = %q, want the inner message plus one CRLF", parts[1].body)
	}
}

func TestBuildAttachmentValidation(t *testing.T) {
	m := baseMessage()
	m.Attachments = []Attachment{{Filename: "", Data: []byte("x")}}
	if _, err := Build(m); err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("empty filename: err = %v", err)
	}
	m.Attachments = []Attachment{{Filename: "a.txt", Data: nil}}
	if _, err := Build(m); err == nil || !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("nil data: err = %v", err)
	}
	m.Attachments = []Attachment{{Filename: "a.txt", Data: []byte{}}}
	if _, err := Build(m); err != nil {
		t.Fatalf("empty (non-nil) data must be allowed: %v", err)
	}
}

func TestBuildWithoutAttachmentsIsSinglePart(t *testing.T) {
	m := baseMessage()
	withNil, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Attachments = []Attachment{}
	withEmpty, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(withNil, withEmpty) {
		t.Fatal("nil and empty attachment lists must produce identical output")
	}
	if strings.Contains(string(withNil), "multipart") {
		t.Fatalf("single-part message must not be multipart:\n%s", withNil)
	}
	msg, body := parse(t, withNil)
	if msg.Header.Get("Content-Type") != "text/plain; charset=utf-8" || body != "See attached\n" {
		t.Fatalf("single part = %q %q", msg.Header.Get("Content-Type"), body)
	}
}
