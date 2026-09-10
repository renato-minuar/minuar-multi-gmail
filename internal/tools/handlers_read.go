package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

func (h *handlers) accountsList(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, AccountsListOutput, error) {
	cfg, err := h.d.Config()
	if err != nil {
		return nil, AccountsListOutput{}, err
	}
	out := AccountsListOutput{Default: cfg.Default, Accounts: make([]AccountOut, 0, len(cfg.Accounts))}
	for _, a := range cfg.Accounts {
		out.Accounts = append(out.Accounts, AccountOut{Alias: a.Alias, Email: a.Email})
	}
	return nil, out, nil
}

func (h *handlers) searchThreads(ctx context.Context, _ *mcp.CallToolRequest, in SearchThreadsInput) (*mcp.CallToolResult, SearchThreadsOutput, error) {
	var zero SearchThreadsOutput
	if err := requireID(in.Query, "query"); err != nil {
		return nil, zero, err
	}
	max, err := boundedInt(in.MaxResults, defaultMaxResults, maxMaxResults, "max_results")
	if err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	res, err := svc.SearchThreads(ctx, gmail.SearchParams{Query: in.Query, MaxResults: int64(max), PageToken: in.PageToken, IncludeSpamTrash: in.IncludeSpamTrash})
	if err != nil {
		return nil, zero, err
	}
	out := SearchThreadsOutput{Account: acct.Alias, Email: acct.Email, NextPageToken: res.NextPageToken, Threads: make([]ThreadRow, 0, len(res.Threads))}
	for _, t := range res.Threads {
		out.Threads = append(out.Threads, ThreadRow{
			ThreadID: t.ThreadID, MessageCount: t.MessageCount, LastMessageAt: formatDate(t.LastMessageAt),
			From: t.From, Subject: t.Subject, Snippet: t.Snippet, Labels: t.Labels, Unread: t.Unread,
		})
	}
	h.log().Info("search_threads", "account", acct.Alias, "results", len(out.Threads))
	return nil, out, nil
}

func toMessageOut(m gmail.Message) MessageOut {
	out := MessageOut{
		MessageID: m.MessageID, ThreadID: m.ThreadID, Date: formatDate(m.Date), From: m.From, To: m.To, Cc: m.Cc,
		ReplyTo: m.ReplyTo, Subject: m.Subject, Labels: m.Labels, BodyText: m.BodyText, BodyTruncated: m.BodyTruncated,
		BodySource: m.BodySource,
	}
	for _, a := range m.Attachments {
		out.Attachments = append(out.Attachments, AttachmentOut{Filename: a.Filename, MimeType: a.MimeType, SizeBytes: a.SizeBytes, AttachmentID: a.AttachmentID})
	}
	return out
}

func (h *handlers) getThread(ctx context.Context, _ *mcp.CallToolRequest, in GetThreadInput) (*mcp.CallToolResult, GetThreadOutput, error) {
	var zero GetThreadOutput
	if err := requireID(in.ThreadID, "thread_id"); err != nil {
		return nil, zero, err
	}
	max, err := boundedInt(in.MaxBodyChars, defaultMaxBodyChar, maxMaxBodyChars, "max_body_chars")
	if err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	th, err := svc.GetThread(ctx, in.ThreadID, max)
	if err != nil {
		return nil, zero, err
	}
	out := GetThreadOutput{Account: acct.Alias, Email: acct.Email, ThreadID: th.ThreadID, Messages: make([]MessageOut, 0, len(th.Messages))}
	for _, m := range th.Messages {
		out.Messages = append(out.Messages, toMessageOut(m))
	}
	h.log().Info("get_thread", "account", acct.Alias, "thread_id", th.ThreadID, "messages", len(out.Messages))
	return nil, out, nil
}

func (h *handlers) getMessage(ctx context.Context, _ *mcp.CallToolRequest, in GetMessageInput) (*mcp.CallToolResult, GetMessageOutput, error) {
	var zero GetMessageOutput
	if err := requireID(in.MessageID, "message_id"); err != nil {
		return nil, zero, err
	}
	max, err := boundedInt(in.MaxBodyChars, defaultMaxBodyChar, maxMaxBodyChars, "max_body_chars")
	if err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	m, err := svc.GetMessage(ctx, in.MessageID, max)
	if err != nil {
		return nil, zero, err
	}
	h.log().Info("get_message", "account", acct.Alias, "message_id", m.MessageID)
	return nil, GetMessageOutput{Account: acct.Alias, Email: acct.Email, Message: toMessageOut(*m)}, nil
}
