package gmail

import (
	"encoding/base64"
	"net/mail"
	"strings"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
)

// decodeBody decodes Gmail's base64url body data, padded or not.
func decodeBody(data string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(data, "="))
}

func headerValue(hs []*gmailapi.MessagePartHeader, name string) string {
	for _, h := range hs {
		if h != nil && strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// splitAddresses splits a To/Cc header. It keeps display names as Gmail
// returned them; when net/mail cannot parse the list it falls back to a
// comma split so nothing is silently dropped.
func splitAddresses(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if list, err := mail.ParseAddressList(v); err == nil {
		out := make([]string, 0, len(list))
		for _, a := range list {
			out = append(out, a.String())
		}
		return out
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// extractBody walks the part tree. First text/plain wins; otherwise the
// first text/html converted to text; otherwise source "none". Parts with
// a filename are attachments and never rendered.
func extractBody(p *gmailapi.MessagePart) (string, string) {
	var plain, html *gmailapi.MessagePart
	var walk func(part *gmailapi.MessagePart)
	walk = func(part *gmailapi.MessagePart) {
		if part == nil {
			return
		}
		if part.Filename == "" && part.Body != nil && part.Body.Data != "" {
			switch {
			case plain == nil && strings.HasPrefix(strings.ToLower(part.MimeType), "text/plain"):
				plain = part
			case html == nil && strings.HasPrefix(strings.ToLower(part.MimeType), "text/html"):
				html = part
			}
		}
		for _, child := range part.Parts {
			if plain != nil {
				return
			}
			walk(child)
		}
	}
	walk(p)
	if plain != nil {
		if b, err := decodeBody(plain.Body.Data); err == nil {
			return string(b), "text/plain"
		}
	}
	if html != nil {
		if b, err := decodeBody(html.Body.Data); err == nil {
			return htmlToText(string(b)), "text/html"
		}
	}
	return "", "none"
}

func collectAttachments(p *gmailapi.MessagePart) []Attachment {
	var out []Attachment
	var walk func(part *gmailapi.MessagePart)
	walk = func(part *gmailapi.MessagePart) {
		if part == nil {
			return
		}
		if part.Filename != "" {
			a := Attachment{Filename: part.Filename, MimeType: part.MimeType}
			keep := true
			if part.Body != nil {
				a.SizeBytes = part.Body.Size
				a.AttachmentID = part.Body.AttachmentId
				if a.AttachmentID == "" && part.Body.Data != "" {
					// Gmail returns small parts inline: no attachment id,
					// the bytes in Body.Data. Undecodable data leaves
					// nothing to forward, so the part is dropped instead of
					// failing the whole render.
					data, err := decodeBody(part.Body.Data)
					if err != nil {
						keep = false
					} else {
						a.Data = data
					}
				}
			}
			if keep {
				out = append(out, a)
			}
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(p)
	return out
}

// truncateRunes cuts s to max runes. max <= 0 means no limit.
func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

func renderMessage(m *gmailapi.Message, names func([]string) []string, maxBodyChars int) Message {
	out := Message{
		MessageID: m.Id,
		ThreadID:  m.ThreadId,
		Date:      time.UnixMilli(m.InternalDate),
		Labels:    names(m.LabelIds),
	}
	if m.Payload != nil {
		hs := m.Payload.Headers
		out.From = headerValue(hs, "From")
		out.To = splitAddresses(headerValue(hs, "To"))
		out.Cc = splitAddresses(headerValue(hs, "Cc"))
		out.ReplyTo = headerValue(hs, "Reply-To")
		out.Subject = headerValue(hs, "Subject")
		text, source := extractBody(m.Payload)
		out.BodyText, out.BodyTruncated = truncateRunes(text, maxBodyChars)
		out.BodySource = source
		out.Attachments = collectAttachments(m.Payload)
	} else {
		out.BodySource = "none"
	}
	return out
}

// summarizeThread builds a search row from a thread fetched with
// format=metadata. Gmail lists messages oldest first.
func summarizeThread(t *gmailapi.Thread, names func([]string) []string) ThreadSummary {
	s := ThreadSummary{ThreadID: t.Id, MessageCount: len(t.Messages), Snippet: t.Snippet}
	if len(t.Messages) == 0 {
		return s
	}
	var ids []string
	seen := map[string]bool{}
	for _, m := range t.Messages {
		for _, id := range m.LabelIds {
			if id == "UNREAD" {
				s.Unread = true
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	s.Labels = names(ids)
	last := t.Messages[len(t.Messages)-1]
	s.LastMessageAt = time.UnixMilli(last.InternalDate)
	if s.Snippet == "" {
		s.Snippet = last.Snippet
	}
	if last.Payload != nil {
		s.From = headerValue(last.Payload.Headers, "From")
		s.Subject = headerValue(last.Payload.Headers, "Subject")
	}
	if s.Subject == "" && t.Messages[0].Payload != nil {
		s.Subject = headerValue(t.Messages[0].Payload.Headers, "Subject")
	}
	return s
}
