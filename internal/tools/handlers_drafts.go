package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
	"github.com/renato-minuar/minuar-multi-gmail/internal/mime"
)

func (h *handlers) createDraft(ctx context.Context, _ *mcp.CallToolRequest, in CreateDraftInput) (*mcp.CallToolResult, CreateDraftOutput, error) {
	var zero CreateDraftOutput
	if strings.TrimSpace(in.BodyText) == "" {
		return nil, zero, errors.New("body_text is required")
	}
	// The paths are checked before any Gmail call and read after the size
	// check, so a bad path or an oversized file costs nothing.
	files, err := checkLocalFiles(in.Attachments)
	if err != nil {
		return nil, zero, err
	}
	if err := checkDraftSize(files, int64(len(in.BodyText))); err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	msg := mime.Message{From: acct.Email, Bcc: in.Bcc, BodyText: in.BodyText, Date: h.now()}
	threadID := ""
	if in.ReplyToMessageID != "" {
		hd, err := svc.GetMessageHeaders(ctx, in.ReplyToMessageID)
		if err != nil {
			return nil, zero, err
		}
		to, cc, err := mime.ReplyRecipients(mime.Original{From: hd.From, ReplyTo: hd.ReplyTo, To: hd.To, Cc: hd.Cc}, acct.Email, in.ReplyAll, in.To)
		if err != nil {
			return nil, zero, err
		}
		extraCc, err := mime.ParseAddresses(in.Cc)
		if err != nil {
			return nil, zero, err
		}
		msg.To = to
		msg.Cc = mime.DedupeCc(cc, to, acct.Email, extraCc)
		msg.Subject = in.Subject
		if strings.TrimSpace(msg.Subject) == "" {
			msg.Subject = mime.ReplySubject(hd.Subject)
		}
		if hd.RFCMessageID != "" {
			msg.InReplyTo = hd.RFCMessageID
			msg.References = append(append([]string{}, hd.References...), hd.RFCMessageID)
		}
		threadID = hd.ThreadID
	} else {
		if len(in.To) == 0 {
			return nil, zero, errors.New("to is required for a new message")
		}
		if strings.TrimSpace(in.Subject) == "" {
			return nil, zero, errors.New("subject is required for a new message")
		}
		if msg.To, err = mime.ParseAddresses(in.To); err != nil {
			return nil, zero, err
		}
		if msg.Cc, err = mime.ParseAddresses(in.Cc); err != nil {
			return nil, zero, err
		}
		msg.Subject = in.Subject
	}
	if msg.Bcc, err = mime.ParseAddresses(in.Bcc); err != nil {
		return nil, zero, err
	}
	var attached []AttachmentOut
	if msg.Attachments, attached, err = readLocalFiles(files); err != nil {
		return nil, zero, err
	}
	raw, err := mime.Build(msg)
	if err != nil {
		return nil, zero, err
	}
	// A draft with files is too large for the JSON raw field and goes
	// through the media upload; a text draft takes the path it always took.
	var res *gmail.DraftResult
	if len(files) > 0 {
		// Second guard: the exact length, headers and encoding included.
		if int64(len(raw)) > maxDraftBytes {
			return nil, zero, fmt.Errorf("the message is %d bytes with its attachments, above Gmail's 25 MB limit", len(raw))
		}
		res, err = svc.CreateDraftUpload(ctx, raw, threadID)
	} else {
		res, err = svc.CreateDraft(ctx, raw, threadID)
	}
	if err != nil {
		return nil, zero, err
	}
	h.log().Info("draft created", "account", acct.Alias, "draft_id", res.DraftID, "recipients", len(msg.To)+len(msg.Cc)+len(msg.Bcc), "reply_to", in.ReplyToMessageID,
		"attachments", len(files), "bytes", len(raw))
	return nil, CreateDraftOutput{
		Account: acct.Alias, Email: acct.Email, DraftID: res.DraftID, MessageID: res.MessageID, ThreadID: res.ThreadID,
		From: acct.Email, To: msg.To, Cc: msg.Cc, Bcc: msg.Bcc, Subject: msg.Subject, Attachments: attached,
	}, nil
}

// addDraftAttachment adds local files to a draft that exists. The draft's
// raw message is fetched, extended by mime.AddAttachments without
// re-encoding anything, and written back with drafts.update, so the draft
// keeps its id, its thread and everything the user wrote.
func (h *handlers) addDraftAttachment(ctx context.Context, _ *mcp.CallToolRequest, in AddDraftAttachmentInput) (*mcp.CallToolResult, AddDraftAttachmentOutput, error) {
	var zero AddDraftAttachmentOutput
	draftID, messageID := strings.TrimSpace(in.DraftID), strings.TrimSpace(in.MessageID)
	if draftID == "" && messageID == "" {
		return nil, zero, errors.New("draft_id or message_id is required")
	}
	if len(in.Attachments) == 0 {
		return nil, zero, errors.New("attachments is required")
	}
	files, err := checkLocalFiles(in.Attachments)
	if err != nil {
		return nil, zero, err
	}
	// Refuse files that are too large on their own before any Gmail call.
	if err := checkDraftSize(files, 0); err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	if draftID == "" {
		draftID, err = svc.FindDraftID(ctx, messageID)
		var nf *gmail.NotFoundError
		if errors.As(err, &nf) {
			return nil, zero, fmt.Errorf("message %s is not a draft. A draft gets a new message id each time it is saved; read the thread again for the current id", messageID)
		}
		if err != nil {
			return nil, zero, err
		}
	}
	draft, err := svc.GetDraftRaw(ctx, draftID)
	if err != nil {
		return nil, zero, err
	}
	if err := checkDraftSize(files, int64(len(draft.Raw))); err != nil {
		return nil, zero, err
	}
	atts, added, err := readLocalFiles(files)
	if err != nil {
		return nil, zero, err
	}
	raw, err := mime.AddAttachments(draft.Raw, atts)
	if err != nil {
		return nil, zero, err
	}
	// Second guard: the exact length of the message that is uploaded.
	if int64(len(raw)) > maxDraftBytes {
		return nil, zero, fmt.Errorf("the message is %d bytes with its attachments, above Gmail's 25 MB limit", len(raw))
	}
	res, err := svc.UpdateDraftUpload(ctx, draftID, raw, draft.ThreadID)
	if err != nil {
		return nil, zero, err
	}
	h.log().Info("draft attachments added", "account", acct.Alias, "draft_id", draftID, "attachments", len(added), "bytes", len(raw))
	return nil, AddDraftAttachmentOutput{
		Account: acct.Alias, Email: acct.Email, DraftID: draftID, MessageID: res.MessageID, ThreadID: res.ThreadID,
		Attachments: added, SizeBytes: int64(len(raw)),
	}, nil
}

func (h *handlers) sendDraft(ctx context.Context, _ *mcp.CallToolRequest, in SendDraftInput) (*mcp.CallToolResult, SendDraftOutput, error) {
	var zero SendDraftOutput
	if err := requireID(in.DraftID, "draft_id"); err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	res, err := svc.SendDraft(ctx, in.DraftID)
	if err != nil {
		return nil, zero, err
	}
	h.log().Info("mail sent", "account", acct.Alias, "draft_id", in.DraftID, "message_id", res.MessageID, "to", res.To, "cc", res.Cc, "subject", res.Subject)
	return nil, SendDraftOutput{Account: acct.Alias, Email: acct.Email, MessageID: res.MessageID, ThreadID: res.ThreadID, To: res.To, Cc: res.Cc, Subject: res.Subject}, nil
}

func (h *handlers) deleteDraft(ctx context.Context, _ *mcp.CallToolRequest, in DeleteDraftInput) (*mcp.CallToolResult, DeleteDraftOutput, error) {
	var zero DeleteDraftOutput
	if err := requireID(in.DraftID, "draft_id"); err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	if err := svc.DeleteDraft(ctx, in.DraftID); err != nil {
		return nil, zero, err
	}
	h.log().Info("draft deleted", "account", acct.Alias, "draft_id", in.DraftID)
	return nil, DeleteDraftOutput{Account: acct.Alias, Email: acct.Email, DraftID: in.DraftID, Deleted: true}, nil
}
