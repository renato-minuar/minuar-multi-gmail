package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/mime"
)

// maxForwardBatch caps one forward_messages call. The forwards run one
// after another and each one downloads its attachments, so the limit keeps
// a single approved call bounded in time and memory.
const maxForwardBatch = 50

// forwardMessages forwards a list of messages to the same recipients under
// one approval prompt. It returns an error only for invalid arguments or a
// failed account resolution: a message that cannot be forwarded is reported
// in its own result and the batch continues.
func (h *handlers) forwardMessages(ctx context.Context, _ *mcp.CallToolRequest, in ForwardMessagesInput) (*mcp.CallToolResult, ForwardMessagesOutput, error) {
	var zero ForwardMessagesOutput
	if len(in.Messages) == 0 {
		return nil, zero, errors.New("messages is required")
	}
	if len(in.Messages) > maxForwardBatch {
		return nil, zero, fmt.Errorf("messages has %d items, above the limit of %d", len(in.Messages), maxForwardBatch)
	}
	ids := make([]string, len(in.Messages))
	seen := make(map[string]bool, len(in.Messages))
	for i, item := range in.Messages {
		id := strings.TrimSpace(item.MessageID)
		if id == "" {
			return nil, zero, fmt.Errorf("messages[%d].message_id is required", i)
		}
		if seen[id] {
			return nil, zero, fmt.Errorf("messages has duplicate message_id %s", id)
		}
		seen[id] = true
		ids[i] = id
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
	// The recipients are the same for every message, so the cc dedupe runs
	// once for the whole batch.
	cc = mime.DedupeCc(nil, to, acct.Email, cc)
	mode := "inline"
	if in.AsEml {
		mode = "eml"
	}

	results := make([]ForwardResult, 0, len(ids))
	// stopped holds the context's error once a failure exposed a cancelled
	// call. The remaining messages are then reported as skipped instead of
	// being attempted against a dead context.
	var stopped string
	for i, id := range ids {
		r := ForwardResult{MessageID: id, Subject: in.Messages[i].Subject, Attachments: []AttachmentOut{}}
		if stopped != "" {
			r.Error = "skipped: " + stopped
			results = append(results, r)
			continue
		}
		out, err := h.forwardOne(ctx, acct, svc, forwardParams{
			MessageID: id,
			To:        to,
			Cc:        cc,
			Bcc:       bcc,
			BodyText:  in.BodyText,
			AsEml:     in.AsEml,
			DraftOnly: in.DraftOnly,
		})
		if err != nil {
			r.Error = err.Error()
			var sendErr *sendFailedError
			if errors.As(err, &sendErr) {
				r.DraftID = sendErr.DraftID
			}
			results = append(results, r)
			if ctxErr := ctx.Err(); ctxErr != nil {
				stopped = ctxErr.Error()
			}
			continue
		}
		r.OK = true
		r.Sent = out.Sent
		r.DraftID = out.DraftID
		r.ThreadID = out.ThreadID
		r.Attachments = out.Attachments
		if out.Sent {
			r.SentID = out.MessageID
		}
		results = append(results, r)
	}

	var sentCount, draftCount, failedCount int
	for _, r := range results {
		switch {
		case !r.OK:
			failedCount++
		case r.Sent:
			sentCount++
		case r.DraftID != "":
			draftCount++
		}
	}
	h.log().Info("forward batch done", "account", acct.Alias, "mode", mode, "requested", len(ids),
		"sent", sentCount, "drafts", draftCount, "failed", failedCount, "recipients", len(to)+len(cc)+len(bcc))
	return nil, ForwardMessagesOutput{
		Account: acct.Alias, Email: acct.Email, Mode: mode, To: to, Cc: cc, Bcc: bcc,
		Results: results, SentCount: sentCount, DraftCount: draftCount, FailedCount: failedCount,
	}, nil
}
