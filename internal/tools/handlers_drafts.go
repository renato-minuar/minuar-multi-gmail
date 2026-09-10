package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/mime"
)

func (h *handlers) createDraft(ctx context.Context, _ *mcp.CallToolRequest, in CreateDraftInput) (*mcp.CallToolResult, CreateDraftOutput, error) {
	var zero CreateDraftOutput
	if strings.TrimSpace(in.BodyText) == "" {
		return nil, zero, errors.New("body_text is required")
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
	raw, err := mime.Build(msg)
	if err != nil {
		return nil, zero, err
	}
	res, err := svc.CreateDraft(ctx, raw, threadID)
	if err != nil {
		return nil, zero, err
	}
	h.log().Info("draft created", "account", acct.Alias, "draft_id", res.DraftID, "recipients", len(msg.To)+len(msg.Cc)+len(msg.Bcc), "reply_to", in.ReplyToMessageID)
	return nil, CreateDraftOutput{
		Account: acct.Alias, Email: acct.Email, DraftID: res.DraftID, MessageID: res.MessageID, ThreadID: res.ThreadID,
		From: acct.Email, To: msg.To, Cc: msg.Cc, Bcc: msg.Bcc, Subject: msg.Subject,
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
