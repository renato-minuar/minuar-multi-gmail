package mime

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	stdmime "mime"
	"mime/multipart"
	"strings"
)

// headerField is one header field of a raw message: its name and its exact
// bytes, folded lines and line breaks included.
type headerField struct {
	name string
	raw  []byte
}

// AddAttachments returns raw, an existing RFC 5322 message, with atts added.
// Nothing of the original is re-encoded. A multipart/mixed message gets the
// new parts before its closing boundary and every other byte stays where it
// is. Any other message becomes the first part of a new multipart/mixed:
// its Content-* header fields move to that part, its other header fields
// (To, Cc, Bcc, Subject, In-Reply-To, References and the rest) stay on the
// message and its body bytes are copied verbatim.
func AddAttachments(raw []byte, atts []Attachment) ([]byte, error) {
	if len(atts) == 0 {
		return nil, errors.New("no attachments to add")
	}
	if err := checkAttachments(atts); err != nil {
		return nil, err
	}
	headEnd, bodyStart := splitMessage(raw)
	fields := splitFields(raw[:headEnd])
	body := raw[bodyStart:]

	if boundary := mixedBoundary(fields); boundary != "" && !carries(atts, boundary) {
		if at, ok := closingDelimiter(body, boundary); ok {
			var parts bytes.Buffer
			if err := writeAttachmentParts(&parts, boundary, atts); err != nil {
				return nil, err
			}
			// The line break before a boundary belongs to the boundary, so
			// the new parts start with one and leave their last one to the
			// closing boundary that follows.
			var b bytes.Buffer
			b.Write(raw[:bodyStart+at])
			b.WriteString("\r\n")
			b.Write(bytes.TrimSuffix(parts.Bytes(), []byte("\r\n")))
			b.Write(body[at:])
			return b.Bytes(), nil
		}
	}

	boundary := newBoundary(raw, atts)
	var b bytes.Buffer
	var entity []headerField
	hasVersion := false
	for _, f := range fields {
		if strings.HasPrefix(strings.ToLower(f.name), "content-") {
			entity = append(entity, f)
			continue
		}
		if strings.EqualFold(f.name, "MIME-Version") {
			hasVersion = true
		}
		writeField(&b, f)
	}
	if !hasVersion {
		b.WriteString("MIME-Version: 1.0\r\n")
	}
	b.WriteString(`Content-Type: multipart/mixed; boundary="` + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	for _, f := range entity {
		writeField(&b, f)
	}
	b.WriteString("\r\n")
	b.Write(body)
	b.WriteString("\r\n")
	if err := writeAttachmentParts(&b, boundary, atts); err != nil {
		return nil, err
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

// splitMessage finds the blank line between header and body. It returns
// where the header ends and where the body starts; a message without a
// blank line is all header.
func splitMessage(raw []byte) (headEnd, bodyStart int) {
	for i := 0; i < len(raw); {
		nl := bytes.IndexByte(raw[i:], '\n')
		if nl < 0 {
			break
		}
		next := i + nl + 1
		if len(bytes.TrimRight(raw[i:next], "\r\n")) == 0 {
			return i, next
		}
		i = next
	}
	return len(raw), len(raw)
}

// splitFields cuts a header block into fields. A line that starts with a
// space or a tab continues the field before it.
func splitFields(head []byte) []headerField {
	var fields []headerField
	for len(head) > 0 {
		end := len(head)
		if nl := bytes.IndexByte(head, '\n'); nl >= 0 {
			end = nl + 1
		}
		line := head[:end]
		head = head[end:]
		if (line[0] == ' ' || line[0] == '\t') && len(fields) > 0 {
			last := &fields[len(fields)-1]
			last.raw = append(last.raw[:len(last.raw):len(last.raw)], line...)
			continue
		}
		name, _, _ := bytes.Cut(line, []byte(":"))
		fields = append(fields, headerField{name: strings.TrimSpace(string(name)), raw: line})
	}
	return fields
}

func writeField(b *bytes.Buffer, f headerField) {
	b.Write(f.raw)
	if !bytes.HasSuffix(f.raw, []byte("\n")) {
		b.WriteString("\r\n")
	}
}

// mixedBoundary returns the boundary of a message whose top-level type is
// multipart/mixed, and "" for every other message.
func mixedBoundary(fields []headerField) string {
	for _, f := range fields {
		if !strings.EqualFold(f.name, "Content-Type") {
			continue
		}
		_, value, _ := bytes.Cut(f.raw, []byte(":"))
		unfolded := strings.NewReplacer("\r", "", "\n", "").Replace(string(value))
		mt, params, err := stdmime.ParseMediaType(strings.TrimSpace(unfolded))
		if err != nil || mt != "multipart/mixed" {
			return ""
		}
		return params["boundary"]
	}
	return ""
}

// closingDelimiter returns the offset in body of the line break before the
// last closing boundary line. Not found means the message is wrapped
// instead of extended.
func closingDelimiter(body []byte, boundary string) (int, bool) {
	delim := []byte("--" + boundary + "--")
	for end := len(body); ; {
		i := bytes.LastIndex(body[:end], delim)
		if i <= 0 {
			return 0, false
		}
		rest := body[i+len(delim):]
		if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		if body[i-1] == '\n' && len(bytes.Trim(rest, " \t\r")) == 0 {
			at := i - 1
			if at > 0 && body[at-1] == '\r' {
				at--
			}
			return at, true
		}
		end = i + len(delim) - 1
	}
}

// carries reports whether an attachment that travels verbatim holds the
// boundary text, which would cut the message in the wrong place.
func carries(atts []Attachment, boundary string) bool {
	for _, a := range atts {
		if strings.EqualFold(a.ContentType, "message/rfc822") && bytes.Contains(a.Data, []byte("--"+boundary)) {
			return true
		}
	}
	return false
}

// newBoundary returns a random boundary that occurs nowhere in the message
// and in no attachment that travels verbatim.
func newBoundary(raw []byte, atts []Attachment) string {
	for {
		boundary := multipart.NewWriter(io.Discard).Boundary()
		if !bytes.Contains(raw, []byte(boundary)) && !carries(atts, boundary) {
			return boundary
		}
	}
}

// writeAttachmentParts writes one part per attachment, each opened by its
// boundary line and ended by the CRLF that belongs to the next boundary.
func writeAttachmentParts(b *bytes.Buffer, boundary string, atts []Attachment) error {
	for _, a := range atts {
		header, enc, err := attachmentHeader(a)
		if err != nil {
			return err
		}
		b.WriteString("--" + boundary + "\r\n")
		for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Transfer-Encoding"} {
			fmt.Fprintf(b, "%s: %s\r\n", name, header.Get(name))
		}
		b.WriteString("\r\n")
		if err := writeAttachmentBody(b, a, enc); err != nil {
			return err
		}
	}
	return nil
}
