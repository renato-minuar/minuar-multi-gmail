// Package tools registers the MCP tools and maps them onto gmail.Service.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// genericAccountDescription is the account property description compiled
// into the input structs. Register replaces it with AccountDescription(cfg)
// unless accounts.json cannot be read.
const genericAccountDescription = "Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."

const (
	noAccountsDescription = "Account alias. No accounts are configured; run minuar-multi-gmail add-account."
	// undescribedAccount stands in for an account whose description is empty.
	undescribedAccount = "use it when the user names this alias or address"
)

// AccountSentences renders one sentence per configured account: alias,
// address and when to use it. The tool schemas and the server instructions
// both embed it, so the model reads one wording, not two.
func AccountSentences(cfg *config.File) string {
	if cfg == nil {
		return noAccountsDescription
	}
	var b strings.Builder
	for _, a := range cfg.Accounts {
		text := strings.TrimRight(strings.TrimSpace(a.Description), ". ")
		if text == "" {
			text = undescribedAccount
		}
		fmt.Fprintf(&b, "'%s' (%s): %s. ", a.Alias, a.Email, text)
	}
	return b.String()
}

// AccountDescription renders the account property description from the
// configured accounts. Register calls it once, at startup: a later edit of
// accounts.json reaches the model at the next server start.
func AccountDescription(cfg *config.File) string {
	if cfg == nil || len(cfg.Accounts) == 0 {
		return noAccountsDescription
	}
	var b strings.Builder
	b.WriteString("Account alias. ")
	if def, ok := cfg.Find(cfg.Default); ok {
		fmt.Fprintf(&b, "Default '%s' (%s). ", def.Alias, def.Email)
	}
	b.WriteString(AccountSentences(cfg))
	b.WriteString("Pick the account from the user's wording and never ask which one.")
	return b.String()
}

// withAccountDescription builds T's input schema and puts desc on its
// account property. It returns any because mcp.Tool.InputSchema is an any:
// a typed nil pointer in that field would not read as "no schema". A nil
// result lets AddTool derive the schema from T, tag descriptions included.
// log receives the two warnings a broken schema can produce; a nil log
// falls back to slog.Default(), the same fallback handlers.log() gives its
// other callers.
func withAccountDescription[T any](desc string, log *slog.Logger) any {
	if log == nil {
		log = slog.Default()
	}
	schema, err := jsonschema.For[T](nil)
	if err != nil || schema == nil {
		log.Warn("build input schema", "type", reflect.TypeFor[T]().String(), "err", err)
		return nil
	}
	prop, ok := schema.Properties["account"]
	if !ok || prop == nil {
		log.Warn("input schema has no account property", "type", reflect.TypeFor[T]().String())
		return nil
	}
	prop.Description = desc
	return schema
}

var ToolNames = []string{
	"accounts_list", "search_threads", "get_thread", "get_message", "get_attachment", "list_labels", "modify_labels",
	"create_draft", "forward_message", "forward_messages", "send_draft", "delete_draft", "trash_thread", "untrash_thread",
}

const (
	defaultMaxResults  = 10
	maxMaxResults      = 50
	defaultMaxBodyChar = 5000
	maxMaxBodyChars    = 100000
)

type Deps struct {
	Config  func() (*config.File, error)
	Service func(ctx context.Context, alias string) (gmail.Service, error)
	Logger  *slog.Logger
	Now     func() time.Time
	// AttachmentDir is where get_attachment writes files, one directory per
	// account and message. Empty disables the tool's writes.
	AttachmentDir string
}

type handlers struct{ d Deps }

func (h *handlers) log() *slog.Logger {
	if h.d.Logger != nil {
		return h.d.Logger
	}
	return slog.Default()
}

func (h *handlers) now() time.Time {
	if h.d.Now != nil {
		return h.d.Now()
	}
	return time.Now()
}

// resolve maps the optional account argument to an account and a service.
func (h *handlers) resolve(ctx context.Context, alias string) (config.Account, gmail.Service, error) {
	cfg, err := h.d.Config()
	if err != nil {
		return config.Account{}, nil, fmt.Errorf("load accounts: %w", err)
	}
	acct, err := cfg.Resolve(strings.TrimSpace(alias))
	if err != nil {
		return config.Account{}, nil, err
	}
	svc, err := h.d.Service(ctx, acct.Alias)
	if err != nil {
		return config.Account{}, nil, err
	}
	return acct, svc, nil
}

func boundedInt(v, def, max int, name string) (int, error) {
	if v == 0 {
		return def, nil
	}
	if v < 1 || v > max {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, max)
	}
	return v, nil
}

func requireID(v, name string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New(name + " is required")
	}
	return nil
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format(time.RFC3339)
}

func boolPtr(b bool) *bool { return &b }

// Register adds the 14 tools to the server. The account property of every
// tool that has one describes the configured accounts, read once here.
func Register(s *mcp.Server, d Deps) {
	h := &handlers{d: d}
	account := genericAccountDescription
	if d.Config != nil {
		cfg, err := d.Config()
		if err != nil {
			h.log().Warn("account guidance falls back to the generic text", "err", err)
		} else {
			account = AccountDescription(cfg)
		}
	}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "accounts_list", Description: "List the connected Gmail accounts and which alias is the default.", Annotations: readOnly}, h.accountsList)
	mcp.AddTool(s, &mcp.Tool{Name: "search_threads", Description: "Search mail threads with Gmail query syntax. Returns compact rows; use get_thread for bodies.", Annotations: readOnly, InputSchema: withAccountDescription[SearchThreadsInput](account, h.log())}, h.searchThreads)
	mcp.AddTool(s, &mcp.Tool{Name: "get_thread", Description: "Read every message in a thread as plain text, with attachment names; get_attachment opens an attachment. Does not mark the thread read.", Annotations: readOnly, InputSchema: withAccountDescription[GetThreadInput](account, h.log())}, h.getThread)
	mcp.AddTool(s, &mcp.Tool{Name: "get_message", Description: "Read one message as plain text.", Annotations: readOnly, InputSchema: withAccountDescription[GetMessageInput](account, h.log())}, h.getMessage)
	mcp.AddTool(s, &mcp.Tool{Name: "get_attachment", Description: "Save the attachments of a message to local files and return their paths, so the files can be opened and read. Saves one attachment when filename is given, every attachment otherwise. Allowed types: " + allowedTypesText() + ". Other types are skipped and reported. Does not mark the message read.", Annotations: readOnly, InputSchema: withAccountDescription[GetAttachmentInput](account, h.log())}, h.getAttachment)
	mcp.AddTool(s, &mcp.Tool{Name: "list_labels", Description: "List the account's labels (system and user).", Annotations: readOnly, InputSchema: withAccountDescription[ListLabelsInput](account, h.log())}, h.listLabels)
	mcp.AddTool(s, &mcp.Tool{Name: "modify_labels", Description: "Add or remove labels on a thread. remove_labels [\"UNREAD\"] marks it read; remove_labels [\"INBOX\"] archives it. Labels are never created.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true}, InputSchema: withAccountDescription[ModifyLabelsInput](account, h.log())}, h.modifyLabels)
	mcp.AddTool(s, &mcp.Tool{Name: "create_draft", Description: "Create a draft, new or as a reply. Nothing is sent. From is always the account's own address.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)}, InputSchema: withAccountDescription[CreateDraftInput](account, h.log())}, h.createDraft)
	mcp.AddTool(s, &mcp.Tool{Name: "forward_message", Description: "Forward an existing message with its attachments. Sends by default; draft_only keeps a draft instead. The original is quoted inline by default; as_eml attaches it as a .eml file and is used only when the user asks for that. Every call asks the user for approval.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}, Meta: mcp.Meta{"anthropic/requiresUserInteraction": true}, InputSchema: withAccountDescription[ForwardMessageInput](account, h.log())}, h.forwardMessage)
	mcp.AddTool(s, &mcp.Tool{Name: "forward_messages", Description: "Forward several messages to the same recipients in one call under one approval. Use this instead of repeated forward_message calls whenever more than one message is to be forwarded. Sends by default; draft_only keeps drafts. Each item's subject is informational and appears in the approval prompt.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}, Meta: mcp.Meta{"anthropic/requiresUserInteraction": true}, InputSchema: withAccountDescription[ForwardMessagesInput](account, h.log())}, h.forwardMessages)
	mcp.AddTool(s, &mcp.Tool{Name: "send_draft", Description: "Send an existing draft. This sends real mail that cannot be recalled.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}, Meta: mcp.Meta{"anthropic/requiresUserInteraction": true}, InputSchema: withAccountDescription[SendDraftInput](account, h.log())}, h.sendDraft)
	mcp.AddTool(s, &mcp.Tool{Name: "delete_draft", Description: "Delete a draft. Affects drafts only, never sent or received mail.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true}, InputSchema: withAccountDescription[DeleteDraftInput](account, h.log())}, h.deleteDraft)
	mcp.AddTool(s, &mcp.Tool{Name: "trash_thread", Description: "Move a thread to trash (reversible with untrash_thread).", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), IdempotentHint: true}, InputSchema: withAccountDescription[TrashThreadInput](account, h.log())}, h.trashThread)
	mcp.AddTool(s, &mcp.Tool{Name: "untrash_thread", Description: "Restore a thread from trash.", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true}, InputSchema: withAccountDescription[UntrashThreadInput](account, h.log())}, h.untrashThread)
}
