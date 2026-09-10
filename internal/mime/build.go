// Package mime builds outgoing RFC 5322 messages and applies the reply
// rules from the spec. It has no network or Gmail dependency.
package mime

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	stdmime "mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// Attachment is one file carried by a Message. ContentType empty means
// application/octet-stream. Data nil is an error; empty is allowed.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type Message struct {
	From        string
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	BodyText    string
	InReplyTo   string
	References  []string
	Date        time.Time
	Attachments []Attachment
}

// ParseAddresses validates every entry with net/mail and returns the
// canonical form (addr.String()).
func ParseAddresses(list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, raw := range list {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		a, err := mail.ParseAddress(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid email address %q: %w", raw, err)
		}
		out = append(out, a.String())
	}
	return out, nil
}

func headerSafe(name, v string) error {
	if strings.ContainsAny(v, "\r\n") {
		return fmt.Errorf("%s must not contain line breaks", name)
	}
	return nil
}

// Build returns the CRLF-terminated message. The body is base64 encoded
// so any UTF-8 text survives every mail path unchanged.
func Build(m Message) ([]byte, error) {
	if strings.TrimSpace(m.From) == "" {
		return nil, errors.New("from is empty")
	}
	if _, err := mail.ParseAddress(m.From); err != nil {
		return nil, fmt.Errorf("invalid from address %q: %w", m.From, err)
	}
	to, err := ParseAddresses(m.To)
	if err != nil {
		return nil, err
	}
	cc, err := ParseAddresses(m.Cc)
	if err != nil {
		return nil, err
	}
	bcc, err := ParseAddresses(m.Bcc)
	if err != nil {
		return nil, err
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		return nil, errors.New("no recipients")
	}
	if strings.TrimSpace(m.Subject) == "" {
		return nil, errors.New("subject is empty")
	}
	if err := headerSafe("subject", m.Subject); err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.BodyText) == "" {
		return nil, errors.New("body is empty")
	}
	if err := headerSafe("in-reply-to", m.InReplyTo); err != nil {
		return nil, err
	}
	for _, r := range m.References {
		if err := headerSafe("references", r); err != nil {
			return nil, err
		}
	}
	for i, a := range m.Attachments {
		if strings.TrimSpace(a.Filename) == "" {
			return nil, fmt.Errorf("attachment %d has no filename", i+1)
		}
		if a.Data == nil {
			return nil, fmt.Errorf("attachment %q has no data", a.Filename)
		}
	}
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}

	var b bytes.Buffer
	w := func(name, value string) { fmt.Fprintf(&b, "%s: %s\r\n", name, value) }
	w("From", m.From)
	if len(to) > 0 {
		w("To", strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		w("Cc", strings.Join(cc, ", "))
	}
	if len(bcc) > 0 {
		w("Bcc", strings.Join(bcc, ", "))
	}
	w("Subject", stdmime.QEncoding.Encode("utf-8", m.Subject))
	w("Date", date.Format(time.RFC1123Z))
	if m.InReplyTo != "" {
		w("In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		w("References", strings.Join(m.References, " "))
	}
	w("MIME-Version", "1.0")
	if len(m.Attachments) == 0 {
		w("Content-Type", "text/plain; charset=utf-8")
		w("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		writeBase64(&b, []byte(m.BodyText))
		return b.Bytes(), nil
	}

	mw := multipart.NewWriter(&b)
	w("Content-Type", `multipart/mixed; boundary="`+mw.Boundary()+`"`)
	b.WriteString("\r\n")
	text, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return nil, err
	}
	writeBase64(text, []byte(m.BodyText))
	for _, a := range m.Attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		ctHeader := stdmime.FormatMediaType(ct, map[string]string{"name": a.Filename})
		disp := stdmime.FormatMediaType("attachment", map[string]string{"filename": a.Filename})
		if ctHeader == "" || disp == "" {
			return nil, fmt.Errorf("attachment %q: cannot encode content type %q", a.Filename, ct)
		}
		// RFC 2046 section 5.2.1 allows only 7bit, 8bit or binary on a
		// message/rfc822 body. Gmail decodes base64 there anyway, Outlook
		// shows such a part as empty, so the message travels verbatim.
		enc := "base64"
		if strings.EqualFold(ct, "message/rfc822") {
			enc = "8bit"
		}
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {ctHeader},
			"Content-Disposition":       {disp},
			"Content-Transfer-Encoding": {enc},
		})
		if err != nil {
			return nil, err
		}
		if enc == "8bit" {
			if _, err := part.Write(a.Data); err != nil {
				return nil, err
			}
			if !bytes.HasSuffix(a.Data, []byte("\r\n")) {
				if _, err := io.WriteString(part, "\r\n"); err != nil {
					return nil, err
				}
			}
			continue
		}
		writeBase64(part, a.Data)
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeBase64 writes data base64 encoded in 76-column CRLF lines, with a
// trailing CRLF. This is the encoding every part of every message uses.
func writeBase64(w io.Writer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		io.WriteString(w, enc[:76])
		io.WriteString(w, "\r\n")
		enc = enc[76:]
	}
	io.WriteString(w, enc)
	io.WriteString(w, "\r\n")
}
