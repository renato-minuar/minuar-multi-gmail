package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
	"github.com/renato-minuar/minuar-multi-gmail/internal/mime"
)

// maxForwardBytes caps the attachment bytes (inline) or raw bytes (eml)
// of one forward. Checked before any download. A var so tests can lower it.
var maxForwardBytes int64 = 100 << 20

const forwardSeparator = "---------- Forwarded message ---------\n"

// sendFailedError reports a draft that exists but could not be sent.
type sendFailedError struct {
	DraftID string
	Err     error
}

func (e *sendFailedError) Error() string {
	return fmt.Sprintf("forward draft %s created but sending failed: %v", e.DraftID, e.Err)
}
func (e *sendFailedError) Unwrap() error { return e.Err }

// forwardParams is one forward's settings, shared by forward_message and
// forward_messages. The address lists are already parsed and canonical:
// the caller validates them and applies the cc dedupe once.
type forwardParams struct {
	MessageID string
	To        []string
	Cc        []string
	Bcc       []string
	BodyText  string
	Subject   string
	AsEml     bool
	DraftOnly bool
}

func (h *handlers) forwardMessage(ctx context.Context, _ *mcp.CallToolRequest, in ForwardMessageInput) (*mcp.CallToolResult, ForwardMessageOutput, error) {
	var zero ForwardMessageOutput
	if err := requireID(in.MessageID, "message_id"); err != nil {
		return nil, zero, err
	}
	to, err := mime.ParseAddresses(in.To)
	if err != nil {
		return nil, zero, err
	}
	if len(to) == 0 {
		return nil, zero, errors.New("to is required")
	}
	cc, err := mime.ParseAddresses(in.Cc)
	if err != nil {
		return nil, zero, err
	}
	bcc, err := mime.ParseAddresses(in.Bcc)
	if err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	out, err := h.forwardOne(ctx, acct, svc, forwardParams{
		MessageID: strings.TrimSpace(in.MessageID),
		To:        to,
		Cc:        mime.DedupeCc(nil, to, acct.Email, cc),
		Bcc:       bcc,
		BodyText:  in.BodyText,
		Subject:   in.Subject,
		AsEml:     in.AsEml,
		DraftOnly: in.DraftOnly,
	})
	if err != nil {
		return nil, zero, err
	}
	return nil, out, nil
}

// forwardOne builds, uploads and (unless DraftOnly) sends one forward. It
// writes the per-message audit line and returns the single-forward output.
func (h *handlers) forwardOne(ctx context.Context, acct config.Account, svc gmail.Service, p forwardParams) (ForwardMessageOutput, error) {
	var zero ForwardMessageOutput
	hd, err := svc.GetMessageHeaders(ctx, p.MessageID)
	if err != nil {
		return zero, err
	}
	msg := mime.Message{From: acct.Email, To: p.To, Cc: p.Cc, Bcc: p.Bcc, Date: h.now()}
	msg.Subject = p.Subject
	if strings.TrimSpace(msg.Subject) == "" {
		msg.Subject = mime.ForwardSubject(hd.Subject)
	}
	if hd.RFCMessageID != "" {
		msg.InReplyTo = hd.RFCMessageID
		msg.References = append(append([]string{}, hd.References...), hd.RFCMessageID)
	}

	mode := "inline"
	atts := []AttachmentOut{}
	// total is the bytes actually attached to the draft: the sum of the
	// downloaded parts in inline mode, the raw length in eml mode. It is
	// what the audit line logs. The cap is a separate number in inline mode
	// because it must be known before any download.
	var total int64
	if p.AsEml {
		mode = "eml"
		// The cap is checked before any download; the estimate from the
		// headers call is the only size known at this point.
		if hd.SizeEstimate > maxForwardBytes {
			return zero, fmt.Errorf("original message is %d bytes, above the 100 MB limit", hd.SizeEstimate)
		}
		rawOrig, err := svc.GetRawMessage(ctx, p.MessageID)
		if err != nil {
			return zero, err
		}
		// Second guard: the exact length, in case the estimate was low.
		total = int64(len(rawOrig))
		if total > maxForwardBytes {
			return zero, fmt.Errorf("original message is %d bytes, above the 100 MB limit", total)
		}
		msg.BodyText = p.BodyText
		if strings.TrimSpace(msg.BodyText) == "" {
			msg.BodyText = "Forwarded message attached."
		}
		name := mime.EmlFilename(hd.Subject)
		msg.Attachments = []mime.Attachment{{Filename: name, ContentType: "message/rfc822", Data: rawOrig}}
		atts = append(atts, AttachmentOut{Filename: name, MimeType: "message/rfc822", SizeBytes: total})
	} else {
		orig, err := svc.GetMessage(ctx, p.MessageID, 0)
		if err != nil {
			return zero, err
		}
		var announced int64
		for _, a := range orig.Attachments {
			announced += a.SizeBytes
		}
		if announced > maxForwardBytes {
			return zero, fmt.Errorf("attachments total %d bytes, above the 100 MB limit", announced)
		}
		msg.BodyText = forwardBody(p.BodyText, orig)
		for _, a := range orig.Attachments {
			var data []byte
			switch {
			case a.AttachmentID != "":
				data, err = svc.GetAttachment(ctx, p.MessageID, a.AttachmentID)
				if err != nil {
					return zero, err
				}
			case a.Data != nil:
				// Gmail delivered the bytes inline; nothing to download.
				data = a.Data
			default:
				return zero, fmt.Errorf("attachment %q has no attachment id", a.Filename)
			}
			msg.Attachments = append(msg.Attachments, mime.Attachment{Filename: a.Filename, ContentType: a.MimeType, Data: data})
			atts = append(atts, AttachmentOut{Filename: a.Filename, MimeType: a.MimeType, SizeBytes: int64(len(data))})
			total += int64(len(data))
		}
	}

	raw, err := mime.Build(msg)
	if err != nil {
		return zero, err
	}
	res, err := svc.CreateDraftUpload(ctx, raw, hd.ThreadID)
	if err != nil {
		return zero, err
	}
	if p.DraftOnly {
		h.log().Info("forward draft created", "account", acct.Alias, "draft_id", res.DraftID, "mode", mode,
			"forwarded", p.MessageID, "recipients", len(msg.To)+len(msg.Cc)+len(msg.Bcc), "attachments", len(atts), "bytes", total)
		return ForwardMessageOutput{
			Account: acct.Alias, Email: acct.Email, DraftID: res.DraftID, MessageID: res.MessageID, ThreadID: res.ThreadID, Sent: false,
			From: acct.Email, To: msg.To, Cc: msg.Cc, Bcc: msg.Bcc, Subject: msg.Subject,
			Mode: mode, ForwardedMessageID: p.MessageID, Attachments: atts,
		}, nil
	}
	sent, err := svc.SendDraft(ctx, res.DraftID)
	if err != nil {
		h.log().Error("forward send failed", "account", acct.Alias, "draft_id", res.DraftID, "err", err)
		return zero, &sendFailedError{DraftID: res.DraftID, Err: err}
	}
	h.log().Info("forward sent", "account", acct.Alias, "draft_id", res.DraftID, "message_id", sent.MessageID, "mode", mode,
		"forwarded", p.MessageID, "to", sent.To, "cc", sent.Cc, "subject", sent.Subject, "attachments", len(atts), "bytes", total)
	return ForwardMessageOutput{
		Account: acct.Alias, Email: acct.Email, MessageID: sent.MessageID, ThreadID: sent.ThreadID, Sent: true,
		From: acct.Email, To: msg.To, Cc: msg.Cc, Bcc: msg.Bcc, Subject: msg.Subject,
		Mode: mode, ForwardedMessageID: p.MessageID, Attachments: atts,
	}, nil
}

// forwardBody composes the note and the Gmail-style forwarded block. The
// separator line matches Gmail's own so mail clients collapse it. An
// empty original body still produces the block.
func forwardBody(note string, o *gmail.Message) string {
	var b strings.Builder
	if strings.TrimSpace(note) != "" {
		b.WriteString(note)
		if !strings.HasSuffix(note, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(forwardSeparator)
	b.WriteString("From: " + o.From + "\n")
	b.WriteString("Date: " + formatDate(o.Date) + "\n")
	b.WriteString("Subject: " + o.Subject + "\n")
	b.WriteString("To: " + strings.Join(o.To, ", ") + "\n")
	if len(o.Cc) > 0 {
		b.WriteString("Cc: " + strings.Join(o.Cc, ", ") + "\n")
	}
	b.WriteString("\n")
	b.WriteString(o.BodyText)
	return b.String()
}
