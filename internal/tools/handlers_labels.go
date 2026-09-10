package tools

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (h *handlers) listLabels(ctx context.Context, _ *mcp.CallToolRequest, in ListLabelsInput) (*mcp.CallToolResult, ListLabelsOutput, error) {
	var zero ListLabelsOutput
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	labels, err := svc.ListLabels(ctx)
	if err != nil {
		return nil, zero, err
	}
	out := ListLabelsOutput{Account: acct.Alias, Email: acct.Email, Labels: make([]LabelOut, 0, len(labels))}
	for _, l := range labels {
		out.Labels = append(out.Labels, LabelOut{ID: l.ID, Name: l.Name, Type: l.Type})
	}
	return nil, out, nil
}

func (h *handlers) modifyLabels(ctx context.Context, _ *mcp.CallToolRequest, in ModifyLabelsInput) (*mcp.CallToolResult, ModifyLabelsOutput, error) {
	var zero ModifyLabelsOutput
	if err := requireID(in.ThreadID, "thread_id"); err != nil {
		return nil, zero, err
	}
	if len(in.AddLabels)+len(in.RemoveLabels) == 0 {
		return nil, zero, errors.New("give at least one of add_labels or remove_labels")
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	after, err := svc.ModifyThreadLabels(ctx, in.ThreadID, in.AddLabels, in.RemoveLabels)
	if err != nil {
		return nil, zero, err
	}
	if after == nil {
		after = []string{}
	}
	h.log().Info("modify_labels", "account", acct.Alias, "thread_id", in.ThreadID, "add", in.AddLabels, "remove", in.RemoveLabels)
	return nil, ModifyLabelsOutput{Account: acct.Alias, Email: acct.Email, ThreadID: in.ThreadID, LabelsAfter: after}, nil
}

func (h *handlers) trashThread(ctx context.Context, _ *mcp.CallToolRequest, in TrashThreadInput) (*mcp.CallToolResult, TrashThreadOutput, error) {
	var zero TrashThreadOutput
	if err := requireID(in.ThreadID, "thread_id"); err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	after, err := svc.TrashThread(ctx, in.ThreadID)
	if err != nil {
		return nil, zero, err
	}
	if after == nil {
		after = []string{}
	}
	h.log().Info("trash_thread", "account", acct.Alias, "thread_id", in.ThreadID)
	return nil, TrashThreadOutput{Account: acct.Alias, Email: acct.Email, ThreadID: in.ThreadID, LabelsAfter: after}, nil
}

func (h *handlers) untrashThread(ctx context.Context, _ *mcp.CallToolRequest, in UntrashThreadInput) (*mcp.CallToolResult, TrashThreadOutput, error) {
	var zero TrashThreadOutput
	if err := requireID(in.ThreadID, "thread_id"); err != nil {
		return nil, zero, err
	}
	acct, svc, err := h.resolve(ctx, in.Account)
	if err != nil {
		return nil, zero, err
	}
	after, err := svc.UntrashThread(ctx, in.ThreadID)
	if err != nil {
		return nil, zero, err
	}
	if after == nil {
		after = []string{}
	}
	h.log().Info("untrash_thread", "account", acct.Alias, "thread_id", in.ThreadID)
	return nil, TrashThreadOutput{Account: acct.Alias, Email: acct.Email, ThreadID: in.ThreadID, LabelsAfter: after}, nil
}
