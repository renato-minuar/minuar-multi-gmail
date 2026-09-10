// Package gmail is the thin typed layer over the Gmail API used by the
// MCP tools: search, read, labels, drafts, send, trash. It renders API
// payloads into compact structs and never logs or stores secrets.
package gmail

import (
	"context"
	"fmt"
	"time"
)

type Attachment struct {
	Filename     string
	MimeType     string
	SizeBytes    int64
	AttachmentID string
	// Data is set only when Gmail returned the bytes inline instead of an
	// attachment id, which it does for small parts. Nil otherwise.
	Data []byte
}

type Message struct {
	MessageID     string
	ThreadID      string
	Date          time.Time
	From          string
	To            []string
	Cc            []string
	ReplyTo       string
	Subject       string
	Labels        []string
	BodyText      string
	BodyTruncated bool
	BodySource    string
	Attachments   []Attachment
}

type Thread struct {
	ThreadID string
	Messages []Message
}

type ThreadSummary struct {
	ThreadID      string
	MessageCount  int
	LastMessageAt time.Time
	From          string
	Subject       string
	Snippet       string
	Labels        []string
	Unread        bool
}

type SearchParams struct {
	Query            string
	MaxResults       int64
	PageToken        string
	IncludeSpamTrash bool
}

type SearchResult struct {
	Threads       []ThreadSummary
	NextPageToken string
}

type MessageHeaders struct {
	MessageID    string
	ThreadID     string
	RFCMessageID string
	References   []string
	Subject      string
	From         string
	ReplyTo      string
	To           []string
	Cc           []string
	// SizeEstimate is Gmail's estimate of the whole message in bytes. It
	// comes free with the metadata call and lets a caller check a size cap
	// before downloading the message.
	SizeEstimate int64
}

type Label struct {
	ID   string
	Name string
	Type string
}

type DraftResult struct {
	DraftID   string
	MessageID string
	ThreadID  string
}

type SendResult struct {
	MessageID string
	ThreadID  string
	To        []string
	Cc        []string
	Subject   string
}

// NotFoundError is returned for a 404 from Gmail.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return "not found: " + e.ID }

// TooLargeError is returned when Gmail rejects an upload for its size.
type TooLargeError struct{ Bytes int }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("message too large for Gmail (%d bytes)", e.Bytes)
}

// Service is what the MCP tools depend on. The real implementation is
// *Client (Task 7); tests use fakes.
type Service interface {
	Email() string
	SearchThreads(ctx context.Context, p SearchParams) (*SearchResult, error)
	GetThread(ctx context.Context, threadID string, maxBodyChars int) (*Thread, error)
	GetMessage(ctx context.Context, messageID string, maxBodyChars int) (*Message, error)
	GetMessageHeaders(ctx context.Context, messageID string) (*MessageHeaders, error)
	ListLabels(ctx context.Context) ([]Label, error)
	ModifyThreadLabels(ctx context.Context, threadID string, add, remove []string) ([]string, error)
	CreateDraft(ctx context.Context, raw []byte, threadID string) (*DraftResult, error)
	CreateDraftUpload(ctx context.Context, raw []byte, threadID string) (*DraftResult, error)
	GetAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error)
	GetRawMessage(ctx context.Context, messageID string) ([]byte, error)
	SendDraft(ctx context.Context, draftID string) (*SendResult, error)
	DeleteDraft(ctx context.Context, draftID string) error
	TrashThread(ctx context.Context, threadID string) ([]string, error)
	UntrashThread(ctx context.Context, threadID string) ([]string, error)
}
