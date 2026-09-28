package tools

// Input structs: fields without omitempty are required in the schema.
// The jsonschema tag is the property description shown to the model.

type SearchThreadsInput struct {
	Account          string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	Query            string `json:"query" jsonschema:"Gmail search query, same syntax as the Gmail search box: from:, to:, subject:, newer_than:7d, has:attachment, label:name, is:unread"`
	MaxResults       int    `json:"max_results,omitempty" jsonschema:"Threads per page, 1-50. Default 10"`
	PageToken        string `json:"page_token,omitempty" jsonschema:"next_page_token from a previous search_threads result"`
	IncludeSpamTrash bool   `json:"include_spam_trash,omitempty" jsonschema:"Also search spam and trash. Default false"`
}

type GetThreadInput struct {
	Account      string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	ThreadID     string `json:"thread_id" jsonschema:"Thread id from search_threads"`
	MaxBodyChars int    `json:"max_body_chars,omitempty" jsonschema:"Body characters per message, 1-100000. Default 5000. Longer bodies are cut and body_truncated is true"`
}

type GetMessageInput struct {
	Account      string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	MessageID    string `json:"message_id" jsonschema:"Message id from get_thread"`
	MaxBodyChars int    `json:"max_body_chars,omitempty" jsonschema:"Body characters, 1-100000. Default 5000"`
}

type GetAttachmentInput struct {
	Account   string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	MessageID string `json:"message_id" jsonschema:"Message id from get_thread or get_message"`
	Filename  string `json:"filename,omitempty" jsonschema:"Attachment filename as get_thread or get_message lists it. Omit to save every attachment of the message"`
}

type ListLabelsInput struct {
	Account string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
}

type ModifyLabelsInput struct {
	Account      string   `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	ThreadID     string   `json:"thread_id" jsonschema:"Thread id to change"`
	AddLabels    []string `json:"add_labels,omitempty" jsonschema:"Label names or ids to add. System ids: INBOX, UNREAD, STARRED, IMPORTANT, SPAM, TRASH"`
	RemoveLabels []string `json:"remove_labels,omitempty" jsonschema:"Label names or ids to remove. Remove UNREAD to mark read, remove INBOX to archive"`
}

type CreateDraftInput struct {
	Account          string   `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	To               []string `json:"to,omitempty" jsonschema:"Recipients. Required for a new message. For a reply, defaults to the original sender"`
	Cc               []string `json:"cc,omitempty" jsonschema:"Cc recipients"`
	Bcc              []string `json:"bcc,omitempty" jsonschema:"Bcc recipients"`
	Subject          string   `json:"subject,omitempty" jsonschema:"Required for a new message. For a reply, defaults to Re: original subject"`
	BodyText         string   `json:"body_text" jsonschema:"Plain text body"`
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty" jsonschema:"Message id to reply to. Sets the thread and the In-Reply-To and References headers"`
	ReplyAll         bool     `json:"reply_all,omitempty" jsonschema:"For replies: also cc the original To and Cc recipients, except this account. Default false"`
}

type ForwardMessageInput struct {
	Account   string   `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	MessageID string   `json:"message_id" jsonschema:"Message id from get_thread or get_message"`
	To        []string `json:"to" jsonschema:"Recipients"`
	Cc        []string `json:"cc,omitempty" jsonschema:"Cc recipients"`
	Bcc       []string `json:"bcc,omitempty" jsonschema:"Bcc recipients"`
	BodyText  string   `json:"body_text,omitempty" jsonschema:"Note placed above the forwarded content. Optional"`
	Subject   string   `json:"subject,omitempty" jsonschema:"Default: Fwd: plus the original subject"`
	AsEml     bool     `json:"as_eml,omitempty" jsonschema:"Attach the original as a .eml file instead of quoting it. Set only when the user asks for eml or to forward as attachment. Default false"`
	DraftOnly bool     `json:"draft_only,omitempty" jsonschema:"Keep the forward as a draft instead of sending it. Default false: the forward is sent. Set true only when the user asks for a draft or wants to review before sending"`
}

// ForwardItem is one message in a forward_messages call. Subject is shown
// to the user in the approval prompt and never used to build the mail.
type ForwardItem struct {
	MessageID string `json:"message_id" jsonschema:"Message id from get_thread or get_message"`
	Subject   string `json:"subject,omitempty" jsonschema:"Informational only: the original subject, shown to the user in the approval prompt. Not used to build the mail"`
}

type ForwardMessagesInput struct {
	Account   string        `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	Messages  []ForwardItem `json:"messages" jsonschema:"Messages to forward, in order. 1 to 50 items"`
	To        []string      `json:"to" jsonschema:"Recipients, the same for every message"`
	Cc        []string      `json:"cc,omitempty" jsonschema:"Cc recipients"`
	Bcc       []string      `json:"bcc,omitempty" jsonschema:"Bcc recipients"`
	BodyText  string        `json:"body_text,omitempty" jsonschema:"Note placed above the forwarded content of every message. Optional"`
	AsEml     bool          `json:"as_eml,omitempty" jsonschema:"Attach each original as a .eml file instead of quoting it. Set only when the user asks for eml or to forward as attachment. Default false"`
	DraftOnly bool          `json:"draft_only,omitempty" jsonschema:"Keep the forwards as drafts instead of sending them. Default false: the forwards are sent. Set true only when the user asks for drafts or wants to review before sending"`
}

type SendDraftInput struct {
	Account string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	DraftID string `json:"draft_id" jsonschema:"Draft id from create_draft"`
}

type DeleteDraftInput struct {
	Account string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	DraftID string `json:"draft_id" jsonschema:"Draft id to delete"`
}

type TrashThreadInput struct {
	Account  string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	ThreadID string `json:"thread_id" jsonschema:"Thread id to move to trash"`
}

type UntrashThreadInput struct {
	Account  string `json:"account,omitempty" jsonschema:"Account alias. Omit for the default account. The server instructions list every alias, its address and when to use it. Never ask the user which one."`
	ThreadID string `json:"thread_id" jsonschema:"Thread id to restore from trash"`
}

// Output structs. Non-omitempty slices must never be nil.

type AccountOut struct {
	Alias string `json:"alias"`
	Email string `json:"email"`
}

type AccountsListOutput struct {
	Default  string       `json:"default,omitempty"`
	Accounts []AccountOut `json:"accounts"`
}

type ThreadRow struct {
	ThreadID      string   `json:"thread_id"`
	MessageCount  int      `json:"message_count"`
	LastMessageAt string   `json:"last_message_at,omitempty"`
	From          string   `json:"from,omitempty"`
	Subject       string   `json:"subject,omitempty"`
	Snippet       string   `json:"snippet,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	Unread        bool     `json:"unread,omitempty"`
}

type SearchThreadsOutput struct {
	Account       string      `json:"account"`
	Email         string      `json:"email"`
	Threads       []ThreadRow `json:"threads"`
	NextPageToken string      `json:"next_page_token,omitempty"`
}

type AttachmentOut struct {
	Filename     string `json:"filename"`
	MimeType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
}

type MessageOut struct {
	MessageID     string          `json:"message_id"`
	ThreadID      string          `json:"thread_id"`
	Date          string          `json:"date,omitempty"`
	From          string          `json:"from,omitempty"`
	To            []string        `json:"to,omitempty"`
	Cc            []string        `json:"cc,omitempty"`
	ReplyTo       string          `json:"reply_to,omitempty"`
	Subject       string          `json:"subject,omitempty"`
	Labels        []string        `json:"labels,omitempty"`
	BodyText      string          `json:"body_text,omitempty"`
	BodyTruncated bool            `json:"body_truncated,omitempty"`
	BodySource    string          `json:"body_source"`
	Attachments   []AttachmentOut `json:"attachments,omitempty"`
}

type GetThreadOutput struct {
	Account  string       `json:"account"`
	Email    string       `json:"email"`
	ThreadID string       `json:"thread_id"`
	Messages []MessageOut `json:"messages"`
}

type GetMessageOutput struct {
	Account string     `json:"account"`
	Email   string     `json:"email"`
	Message MessageOut `json:"message"`
}

// SavedAttachment is one file get_attachment wrote. Filename is the name in
// the mail; Path is where the file is, under a name made safe for disk.
type SavedAttachment struct {
	Filename  string `json:"filename"`
	Path      string `json:"path"`
	MimeType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
}

type SkippedAttachment struct {
	Filename string `json:"filename"`
	Reason   string `json:"reason"`
}

type GetAttachmentOutput struct {
	Account   string              `json:"account"`
	Email     string              `json:"email"`
	MessageID string              `json:"message_id"`
	Files     []SavedAttachment   `json:"files"`
	Skipped   []SkippedAttachment `json:"skipped"`
}

type LabelOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type ListLabelsOutput struct {
	Account string     `json:"account"`
	Email   string     `json:"email"`
	Labels  []LabelOut `json:"labels"`
}

type ModifyLabelsOutput struct {
	Account     string   `json:"account"`
	Email       string   `json:"email"`
	ThreadID    string   `json:"thread_id"`
	LabelsAfter []string `json:"labels_after"`
}

type CreateDraftOutput struct {
	Account   string   `json:"account"`
	Email     string   `json:"email"`
	DraftID   string   `json:"draft_id"`
	MessageID string   `json:"message_id,omitempty"`
	ThreadID  string   `json:"thread_id,omitempty"`
	From      string   `json:"from"`
	To        []string `json:"to,omitempty"`
	Cc        []string `json:"cc,omitempty"`
	Bcc       []string `json:"bcc,omitempty"`
	Subject   string   `json:"subject"`
}

type ForwardMessageOutput struct {
	Account            string          `json:"account"`
	Email              string          `json:"email"`
	DraftID            string          `json:"draft_id,omitempty"`
	MessageID          string          `json:"message_id,omitempty"`
	ThreadID           string          `json:"thread_id,omitempty"`
	Sent               bool            `json:"sent"`
	From               string          `json:"from"`
	To                 []string        `json:"to,omitempty"`
	Cc                 []string        `json:"cc,omitempty"`
	Bcc                []string        `json:"bcc,omitempty"`
	Subject            string          `json:"subject"`
	Mode               string          `json:"mode"`
	ForwardedMessageID string          `json:"forwarded_message_id"`
	Attachments        []AttachmentOut `json:"attachments"`
}

// ForwardResult is one message's outcome inside a batch forward. A failed
// item carries Error and stops nothing: the rest of the batch continues.
type ForwardResult struct {
	MessageID   string          `json:"message_id"`
	Subject     string          `json:"subject,omitempty"`
	OK          bool            `json:"ok"`
	Error       string          `json:"error,omitempty"`
	Sent        bool            `json:"sent"`
	DraftID     string          `json:"draft_id,omitempty"`
	SentID      string          `json:"sent_message_id,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	Attachments []AttachmentOut `json:"attachments"`
}

type ForwardMessagesOutput struct {
	Account     string          `json:"account"`
	Email       string          `json:"email"`
	Mode        string          `json:"mode"`
	To          []string        `json:"to,omitempty"`
	Cc          []string        `json:"cc,omitempty"`
	Bcc         []string        `json:"bcc,omitempty"`
	Results     []ForwardResult `json:"results"`
	SentCount   int             `json:"sent_count"`
	DraftCount  int             `json:"draft_count"`
	FailedCount int             `json:"failed_count"`
}

type SendDraftOutput struct {
	Account   string   `json:"account"`
	Email     string   `json:"email"`
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id,omitempty"`
	To        []string `json:"to,omitempty"`
	Cc        []string `json:"cc,omitempty"`
	Subject   string   `json:"subject,omitempty"`
}

type DeleteDraftOutput struct {
	Account string `json:"account"`
	Email   string `json:"email"`
	DraftID string `json:"draft_id"`
	Deleted bool   `json:"deleted"`
}

type TrashThreadOutput struct {
	Account     string   `json:"account"`
	Email       string   `json:"email"`
	ThreadID    string   `json:"thread_id"`
	LabelsAfter []string `json:"labels_after"`
}
