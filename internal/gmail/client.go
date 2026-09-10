package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const MaxParallel = 5

// CallTimeout bounds one ordinary Gmail call. TransferTimeout bounds the
// three calls that move a whole message or file over the wire — attachment
// download, raw message fetch and draft upload — which carry up to 100 MB
// and need more than a metadata call on a slow link. Both are vars so tests
// can lower them; production never assigns them.
var (
	CallTimeout     = 30 * time.Second
	TransferTimeout = 5 * time.Minute
)

var metadataHeaders = []string{"From", "To", "Cc", "Subject", "Date"}

// Client implements Service against the real Gmail API for one account.
type Client struct {
	svc    *gmailapi.Service
	alias  string
	email  string
	sleep  func(time.Duration)
	sem    chan struct{}
	labels labelCache
}

// New builds a client. Callers pass option.WithTokenSource(ts) in
// production and endpoint overrides in tests.
func New(ctx context.Context, alias, email string, opts ...option.ClientOption) (*Client, error) {
	svc, err := gmailapi.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gmail service for %s: %w", alias, err)
	}
	return &Client{svc: svc, alias: alias, email: email, sleep: time.Sleep, sem: make(chan struct{}, MaxParallel)}, nil
}

// sleepDefault backs the retry pause for package-level helpers (ProfileEmail)
// that have no *Client to hold an injectable sleep function. Tests in this
// package override it directly.
var sleepDefault = time.Sleep

// ProfileEmail returns the address of the authenticated account. It uses
// the same call timeout and single retry on 429/5xx as every Client call.
func ProfileEmail(ctx context.Context, opts ...option.ClientOption) (string, error) {
	svc, err := gmailapi.NewService(ctx, opts...)
	if err != nil {
		return "", err
	}
	var p *gmailapi.Profile
	err = callWithRetry(ctx, CallTimeout, sleepDefault, func(cctx context.Context) error {
		var err error
		p, err = svc.Users.GetProfile("me").Context(cctx).Do()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("get profile: %w", err)
	}
	if p.EmailAddress == "" {
		return "", errors.New("get profile: empty email address")
	}
	return p.EmailAddress, nil
}

func (c *Client) Email() string { return c.email }

func retryable(err error) bool {
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		return ge.Code == http.StatusTooManyRequests || ge.Code >= 500
	}
	return false
}

// do runs fn under the call timeout and the parallelism cap, retrying
// once on 429 or 5xx.
func (c *Client) do(ctx context.Context, fn func(ctx context.Context) error) error {
	return c.run(ctx, CallTimeout, fn)
}

// doTransfer is do with the transfer timeout: same parallelism cap and
// same single retry, a longer bound for calls that carry a whole file.
func (c *Client) doTransfer(ctx context.Context, fn func(ctx context.Context) error) error {
	return c.run(ctx, TransferTimeout, fn)
}

func (c *Client) run(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sem }()
	return callWithRetry(ctx, timeout, c.sleep, fn)
}

// callWithRetry runs fn under the given timeout, retrying once on a
// retryable (429 or 5xx) error after a 1s-plus-jitter pause. It has no
// dependency on *Client so package-level helpers such as ProfileEmail can
// share it.
func callWithRetry(ctx context.Context, timeout time.Duration, sleep func(time.Duration), fn func(ctx context.Context) error) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		err = fn(cctx)
		cancel()
		if err == nil || !retryable(err) || attempt == 1 {
			return err
		}
		sleep(time.Second + time.Duration(rand.IntN(500))*time.Millisecond)
	}
	return err
}

// mapErr translates Google errors: 404 -> NotFoundError, 401 or 403
// insufficient permissions -> ReauthError, everything else unchanged.
func (c *Client) mapErr(err error, id string) error {
	if err == nil {
		return nil
	}
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return err
	}
	switch {
	case ge.Code == http.StatusNotFound:
		return &NotFoundError{ID: id}
	case ge.Code == http.StatusUnauthorized:
		return &googleauth.ReauthError{Alias: c.alias, Email: c.email, Reason: "Gmail rejected the token"}
	case ge.Code == http.StatusForbidden && insufficientPermissions(ge):
		return &googleauth.ReauthError{Alias: c.alias, Email: c.email, Reason: "scope changed"}
	}
	return err
}

func insufficientPermissions(ge *googleapi.Error) bool {
	for _, item := range ge.Errors {
		if item.Reason == "insufficientPermissions" {
			return true
		}
	}
	return strings.Contains(strings.ToLower(ge.Message), "insufficient")
}

func (c *Client) SearchThreads(ctx context.Context, p SearchParams) (*SearchResult, error) {
	if err := c.loadLabels(ctx, false); err != nil {
		return nil, err
	}
	var list *gmailapi.ListThreadsResponse
	err := c.do(ctx, func(ctx context.Context) error {
		call := c.svc.Users.Threads.List("me").Q(p.Query).MaxResults(p.MaxResults).IncludeSpamTrash(p.IncludeSpamTrash)
		if p.PageToken != "" {
			call = call.PageToken(p.PageToken)
		}
		var err error
		list, err = call.Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, "")
	}
	res := &SearchResult{NextPageToken: list.NextPageToken, Threads: make([]ThreadSummary, len(list.Threads))}
	errs := make([]error, len(list.Threads))
	done := make(chan struct{})
	for i, th := range list.Threads {
		go func(i int, id string) {
			defer func() { done <- struct{}{} }()
			var full *gmailapi.Thread
			err := c.do(ctx, func(ctx context.Context) error {
				var err error
				full, err = c.svc.Users.Threads.Get("me", id).Format("metadata").MetadataHeaders(metadataHeaders...).Context(ctx).Do()
				return err
			})
			if err != nil {
				errs[i] = c.mapErr(err, id)
				return
			}
			res.Threads[i] = summarizeThread(full, c.labelNames)
		}(i, th.Id)
	}
	for range list.Threads {
		<-done
	}
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return res, nil
}

func (c *Client) GetThread(ctx context.Context, threadID string, maxBodyChars int) (*Thread, error) {
	if err := c.loadLabels(ctx, false); err != nil {
		return nil, err
	}
	var th *gmailapi.Thread
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		th, err = c.svc.Users.Threads.Get("me", threadID).Format("full").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, threadID)
	}
	out := &Thread{ThreadID: th.Id, Messages: make([]Message, 0, len(th.Messages))}
	for _, m := range th.Messages {
		out.Messages = append(out.Messages, renderMessage(m, c.labelNames, maxBodyChars))
	}
	return out, nil
}

func (c *Client) GetMessage(ctx context.Context, messageID string, maxBodyChars int) (*Message, error) {
	if err := c.loadLabels(ctx, false); err != nil {
		return nil, err
	}
	var m *gmailapi.Message
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		m, err = c.svc.Users.Messages.Get("me", messageID).Format("full").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, messageID)
	}
	msg := renderMessage(m, c.labelNames, maxBodyChars)
	return &msg, nil
}

func (c *Client) GetMessageHeaders(ctx context.Context, messageID string) (*MessageHeaders, error) {
	var m *gmailapi.Message
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		m, err = c.svc.Users.Messages.Get("me", messageID).Format("metadata").
			MetadataHeaders("Message-ID", "References", "Subject", "From", "To", "Cc", "Reply-To").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, messageID)
	}
	h := &MessageHeaders{MessageID: m.Id, ThreadID: m.ThreadId, SizeEstimate: m.SizeEstimate}
	if m.Payload != nil {
		hs := m.Payload.Headers
		h.RFCMessageID = strings.TrimSpace(headerValue(hs, "Message-ID"))
		h.References = strings.Fields(headerValue(hs, "References"))
		h.Subject = headerValue(hs, "Subject")
		h.From = headerValue(hs, "From")
		h.ReplyTo = headerValue(hs, "Reply-To")
		h.To = splitAddresses(headerValue(hs, "To"))
		h.Cc = splitAddresses(headerValue(hs, "Cc"))
	}
	return h, nil
}

func (c *Client) ListLabels(ctx context.Context) ([]Label, error) {
	if err := c.loadLabels(ctx, true); err != nil {
		return nil, err
	}
	c.labels.mu.Lock()
	defer c.labels.mu.Unlock()
	out := make([]Label, len(c.labels.labels))
	copy(out, c.labels.labels)
	return out, nil
}

func (c *Client) threadLabelNames(th *gmailapi.Thread) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range th.Messages {
		for _, id := range m.LabelIds {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return c.labelNames(ids)
}

func (c *Client) ModifyThreadLabels(ctx context.Context, threadID string, add, remove []string) ([]string, error) {
	if len(add)+len(remove) == 0 {
		return nil, errors.New("modify_labels: give at least one label to add or remove")
	}
	addIDs, err := c.resolveLabelIDs(ctx, add)
	if err != nil {
		return nil, err
	}
	removeIDs, err := c.resolveLabelIDs(ctx, remove)
	if err != nil {
		return nil, err
	}
	var th *gmailapi.Thread
	err = c.do(ctx, func(ctx context.Context) error {
		var err error
		th, err = c.svc.Users.Threads.Modify("me", threadID, &gmailapi.ModifyThreadRequest{AddLabelIds: addIDs, RemoveLabelIds: removeIDs}).Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, threadID)
	}
	return c.threadLabelNames(th), nil
}

func draftResult(out *gmailapi.Draft) *DraftResult {
	res := &DraftResult{DraftID: out.Id}
	if out.Message != nil {
		res.MessageID = out.Message.Id
		res.ThreadID = out.Message.ThreadId
	}
	return res
}

func (c *Client) CreateDraft(ctx context.Context, raw []byte, threadID string) (*DraftResult, error) {
	draft := &gmailapi.Draft{Message: &gmailapi.Message{Raw: base64.RawURLEncoding.EncodeToString(raw), ThreadId: threadID}}
	var out *gmailapi.Draft
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		out, err = c.svc.Users.Drafts.Create("me", draft).Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, threadID)
	}
	return draftResult(out), nil
}

// CreateDraftUpload creates a draft through the media upload path: the
// raw RFC 5322 message travels as message/rfc822 media and the JSON
// metadata carries only the thread id. Used for drafts with attachments,
// which are too large for the JSON raw field. A fresh reader is built on
// every attempt so the retry in doTransfer() re-sends the whole body.
func (c *Client) CreateDraftUpload(ctx context.Context, raw []byte, threadID string) (*DraftResult, error) {
	draft := &gmailapi.Draft{Message: &gmailapi.Message{ThreadId: threadID}}
	var out *gmailapi.Draft
	err := c.doTransfer(ctx, func(ctx context.Context) error {
		var err error
		out, err = c.svc.Users.Drafts.Create("me", draft).
			Media(bytes.NewReader(raw), googleapi.ContentType("message/rfc822")).
			Context(ctx).Do()
		return err
	})
	if err != nil {
		if tooLarge(err) {
			return nil, &TooLargeError{Bytes: len(raw)}
		}
		return nil, c.mapErr(err, threadID)
	}
	return draftResult(out), nil
}

// tooLarge reports a Google rejection of an upload for its size: HTTP 413
// or an error message containing "too large".
func tooLarge(err error) bool {
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	return ge.Code == http.StatusRequestEntityTooLarge || strings.Contains(strings.ToLower(ge.Message), "too large")
}

// GetAttachment downloads one attachment and returns its decoded bytes.
// It runs under the transfer timeout: an attachment can be up to 100 MB.
func (c *Client) GetAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	var body *gmailapi.MessagePartBody
	err := c.doTransfer(ctx, func(ctx context.Context) error {
		var err error
		body, err = c.svc.Users.Messages.Attachments.Get("me", messageID, attachmentID).Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, attachmentID)
	}
	data, err := decodeBody(body.Data)
	if err != nil {
		return nil, fmt.Errorf("decode attachment %s: %w", attachmentID, err)
	}
	return data, nil
}

// GetRawMessage fetches a message with format=raw and returns the decoded
// RFC 5322 bytes, the form an eml forward attaches. Like GetAttachment it
// runs under the transfer timeout.
func (c *Client) GetRawMessage(ctx context.Context, messageID string) ([]byte, error) {
	var m *gmailapi.Message
	err := c.doTransfer(ctx, func(ctx context.Context) error {
		var err error
		m, err = c.svc.Users.Messages.Get("me", messageID).Format("raw").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, messageID)
	}
	raw, err := decodeBody(m.Raw)
	if err != nil {
		return nil, fmt.Errorf("decode raw message %s: %w", messageID, err)
	}
	return raw, nil
}

// SendDraft reads the draft's recipients first so the caller can log an
// exact audit line, then sends. A missing draft fails before any send.
func (c *Client) SendDraft(ctx context.Context, draftID string) (*SendResult, error) {
	var d *gmailapi.Draft
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		d, err = c.svc.Users.Drafts.Get("me", draftID).Format("metadata").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, draftID)
	}
	res := &SendResult{}
	if d.Message != nil && d.Message.Payload != nil {
		hs := d.Message.Payload.Headers
		res.To = splitAddresses(headerValue(hs, "To"))
		res.Cc = splitAddresses(headerValue(hs, "Cc"))
		res.Subject = headerValue(hs, "Subject")
	}
	var sent *gmailapi.Message
	err = c.do(ctx, func(ctx context.Context) error {
		var err error
		sent, err = c.svc.Users.Drafts.Send("me", &gmailapi.Draft{Id: draftID}).Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, draftID)
	}
	res.MessageID = sent.Id
	res.ThreadID = sent.ThreadId
	return res, nil
}

func (c *Client) DeleteDraft(ctx context.Context, draftID string) error {
	err := c.do(ctx, func(ctx context.Context) error {
		return c.svc.Users.Drafts.Delete("me", draftID).Context(ctx).Do()
	})
	return c.mapErr(err, draftID)
}

func (c *Client) TrashThread(ctx context.Context, threadID string) ([]string, error) {
	var th *gmailapi.Thread
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		th, err = c.svc.Users.Threads.Trash("me", threadID).Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, threadID)
	}
	return c.threadLabelNames(th), nil
}

func (c *Client) UntrashThread(ctx context.Context, threadID string) ([]string, error) {
	var th *gmailapi.Thread
	err := c.do(ctx, func(ctx context.Context) error {
		var err error
		th, err = c.svc.Users.Threads.Untrash("me", threadID).Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, c.mapErr(err, threadID)
	}
	return c.threadLabelNames(th), nil
}

var _ Service = (*Client)(nil)
