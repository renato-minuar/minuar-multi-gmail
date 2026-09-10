package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
)

// headersFor copies the fixture headers once per message id so a batch
// resolves every item to its own headers.
func headersFor(base *gmail.MessageHeaders, ids ...string) map[string]*gmail.MessageHeaders {
	m := make(map[string]*gmail.MessageHeaders, len(ids))
	for _, id := range ids {
		hd := *base
		hd.MessageID = id
		m[id] = &hd
	}
	return m
}

// wantBatchCalls is the call sequence one inline forward makes, repeated
// for every id, with the send call when sent is true.
func wantBatchCalls(sent bool, ids ...string) string {
	var parts []string
	for _, id := range ids {
		one := fmt.Sprintf("headers:%s,message:%s,att:att-1,att:att-2,upload", id, id)
		if sent {
			one += ",send:d-2"
		}
		parts = append(parts, one)
	}
	return strings.Join(parts, ",")
}

func TestForwardMessagesSendsEveryMessage(t *testing.T) {
	e := forwardFixture(t)
	e.work.headersByID = headersFor(e.work.headers, "m-1", "m-2", "m-3")
	_, out, err := e.h.forwardMessages(context.Background(), nil, ForwardMessagesInput{
		Messages: []ForwardItem{{MessageID: "m-1", Subject: "One"}, {MessageID: "m-2", Subject: "Two"}, {MessageID: "m-3", Subject: "Three"}},
		To:       []string{"dave@example.com"}, Cc: []string{"erin@example.com"}, BodyText: "FYI",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(e.work.calls, ","), wantBatchCalls(true, "m-1", "m-2", "m-3"); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
	if out.Account != "work" || out.Email != "control@example.com" || out.Mode != "inline" {
		t.Fatalf("out = %+v", out)
	}
	if strings.Join(out.To, ",") != "<dave@example.com>" || strings.Join(out.Cc, ",") != "<erin@example.com>" {
		t.Fatalf("recipients = %v / %v", out.To, out.Cc)
	}
	if out.SentCount != 3 || out.DraftCount != 0 || out.FailedCount != 0 {
		t.Fatalf("counts = %d/%d/%d", out.SentCount, out.DraftCount, out.FailedCount)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results = %+v", out.Results)
	}
	for i, want := range []struct{ id, subject string }{{"m-1", "One"}, {"m-2", "Two"}, {"m-3", "Three"}} {
		r := out.Results[i]
		if r.MessageID != want.id || r.Subject != want.subject {
			t.Fatalf("result %d = %+v, want %s/%s", i, r, want.id, want.subject)
		}
		if !r.OK || !r.Sent || r.Error != "" || r.SentID != "m-9" || r.ThreadID != "t-9" || r.DraftID != "" {
			t.Fatalf("result %d = %+v", i, r)
		}
		if len(r.Attachments) != 2 || r.Attachments[0].Filename != "invoice.pdf" || r.Attachments[1].Filename != "notes.txt" {
			t.Fatalf("result %d attachments = %+v", i, r.Attachments)
		}
	}
	if len(e.work.uploads) != 3 {
		t.Fatalf("uploads = %d, want one draft per message", len(e.work.uploads))
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "forward batch done") {
		t.Fatalf("logs = %s", logs)
	}
	if n := strings.Count(logs, "forward sent"); n != 3 {
		t.Fatalf("forward sent lines = %d, want 3\n%s", n, logs)
	}
	if strings.Contains(logs, "second line") || strings.Contains(logs, "FYI") {
		t.Fatalf("logs must not carry body text: %s", logs)
	}
}

// TestForwardMessagesContinuesAfterFailure proves one unusable message
// does not stop the batch and does not fail the call.
func TestForwardMessagesContinuesAfterFailure(t *testing.T) {
	e := forwardFixture(t)
	e.work.headersByID = headersFor(e.work.headers, "m-1", "m-3")
	_, out, err := e.h.forwardMessages(context.Background(), nil, ForwardMessagesInput{
		Messages: []ForwardItem{{MessageID: "m-1"}, {MessageID: "m-2"}, {MessageID: "m-3"}},
		To:       []string{"dave@example.com"},
	})
	if err != nil {
		t.Fatalf("a failed item must not fail the call: %v", err)
	}
	if out.SentCount != 2 || out.DraftCount != 0 || out.FailedCount != 1 {
		t.Fatalf("counts = %d/%d/%d", out.SentCount, out.DraftCount, out.FailedCount)
	}
	bad := out.Results[1]
	if bad.MessageID != "m-2" || bad.OK || bad.Sent || !strings.Contains(bad.Error, "not found: m-2") {
		t.Fatalf("result 1 = %+v", bad)
	}
	if bad.Attachments == nil || len(bad.Attachments) != 0 {
		t.Fatalf("a failed result needs an empty, non-nil attachment list: %#v", bad.Attachments)
	}
	if !out.Results[0].Sent || !out.Results[2].Sent {
		t.Fatalf("results = %+v", out.Results)
	}
	if got, want := strings.Join(e.work.calls, ","), "headers:m-1,message:m-1,att:att-1,att:att-2,upload,send:d-2,headers:m-2,headers:m-3,message:m-3,att:att-1,att:att-2,upload,send:d-2"; got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

// TestForwardMessagesSkipsAfterCancellation proves a cancelled context
// stops the loop and records the untouched messages as skipped.
func TestForwardMessagesSkipsAfterCancellation(t *testing.T) {
	e := forwardFixture(t)
	e.work.headersByID = headersFor(e.work.headers, "m-2", "m-3")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, out, err := e.h.forwardMessages(ctx, nil, ForwardMessagesInput{
		Messages: []ForwardItem{{MessageID: "m-1"}, {MessageID: "m-2"}, {MessageID: "m-3"}},
		To:       []string{"dave@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.FailedCount != 3 || out.SentCount != 0 {
		t.Fatalf("counts = %d/%d", out.FailedCount, out.SentCount)
	}
	for i := 1; i < 3; i++ {
		if out.Results[i].OK || out.Results[i].Error != "skipped: context canceled" {
			t.Fatalf("result %d = %+v", i, out.Results[i])
		}
	}
	if strings.Join(e.work.calls, ",") != "headers:m-1" {
		t.Fatalf("calls = %v, want the loop stopped after the first failure", e.work.calls)
	}
}

func TestForwardMessagesDraftOnly(t *testing.T) {
	e := forwardFixture(t)
	e.work.headersByID = headersFor(e.work.headers, "m-1", "m-2", "m-3")
	_, out, err := e.h.forwardMessages(context.Background(), nil, ForwardMessagesInput{
		Messages:  []ForwardItem{{MessageID: "m-1"}, {MessageID: "m-2"}, {MessageID: "m-3"}},
		To:        []string{"dave@example.com"},
		DraftOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(e.work.calls, ","), wantBatchCalls(false, "m-1", "m-2", "m-3"); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
	if out.DraftCount != 3 || out.SentCount != 0 || out.FailedCount != 0 {
		t.Fatalf("counts = %d/%d/%d", out.DraftCount, out.SentCount, out.FailedCount)
	}
	for i, r := range out.Results {
		if !r.OK || r.Sent || r.DraftID != "d-2" || r.SentID != "" || r.ThreadID != "t-10" {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	if !strings.Contains(e.logs.String(), "forward batch done") {
		t.Fatalf("logs = %s", e.logs.String())
	}
}

// TestForwardMessagesSendFailureKeepsDraftID proves a send failure inside
// the batch exposes the draft id on the failed result, not just in the
// error text, so send_draft can finish the draft without parsing it.
func TestForwardMessagesSendFailureKeepsDraftID(t *testing.T) {
	e := forwardFixture(t)
	e.work.headersByID = headersFor(e.work.headers, "m-1", "m-2")
	e.work.sendErr = errors.New("smtp down")
	_, out, err := e.h.forwardMessages(context.Background(), nil, ForwardMessagesInput{
		Messages: []ForwardItem{{MessageID: "m-1"}, {MessageID: "m-2"}},
		To:       []string{"dave@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SentCount != 0 || out.FailedCount != 2 {
		t.Fatalf("counts = %d/%d", out.SentCount, out.FailedCount)
	}
	for i, r := range out.Results {
		if r.OK || r.DraftID != "d-2" {
			t.Fatalf("result %d = %+v", i, r)
		}
		if !strings.Contains(r.Error, "forward draft d-2 created but sending failed") || !strings.Contains(r.Error, "smtp down") {
			t.Fatalf("result %d error = %q", i, r.Error)
		}
	}
}

func TestForwardMessagesValidation(t *testing.T) {
	e := forwardFixture(t)
	many := make([]ForwardItem, 51)
	for i := range many {
		many[i] = ForwardItem{MessageID: fmt.Sprintf("m-%d", i)}
	}
	cases := []struct {
		name  string
		in    ForwardMessagesInput
		want  string
		exact bool
	}{
		{"empty list", ForwardMessagesInput{To: []string{"d@example.com"}}, "messages is required", true},
		{"over the limit", ForwardMessagesInput{Messages: many, To: []string{"d@example.com"}}, "messages has 51 items, above the limit of 50", true},
		{"blank id", ForwardMessagesInput{Messages: []ForwardItem{{MessageID: "m-1"}, {MessageID: " "}}, To: []string{"d@example.com"}}, "messages[1].message_id is required", true},
		{"duplicate id", ForwardMessagesInput{Messages: []ForwardItem{{MessageID: "m-1"}, {MessageID: "m-1"}}, To: []string{"d@example.com"}}, "messages has duplicate message_id m-1", true},
		{"missing to", ForwardMessagesInput{Messages: []ForwardItem{{MessageID: "m-1"}}}, "to is required", true},
		{"blank to", ForwardMessagesInput{Messages: []ForwardItem{{MessageID: "m-1"}}, To: []string{" "}}, "to is required", true},
		{"bad cc", ForwardMessagesInput{Messages: []ForwardItem{{MessageID: "m-1"}}, To: []string{"d@example.com"}, Cc: []string{"nope"}}, "invalid email address", false},
	}
	for _, c := range cases {
		_, _, err := e.h.forwardMessages(context.Background(), nil, c.in)
		if err == nil {
			t.Errorf("%s: err = nil, want %q", c.name, c.want)
			continue
		}
		if c.exact {
			if err.Error() != c.want {
				t.Errorf("%s: err = %v, want exactly %q", c.name, err, c.want)
			}
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if len(e.asked) != 0 {
		t.Fatalf("validation must fail before any service is built: %v", e.asked)
	}
	if len(e.work.calls) != 0 {
		t.Fatalf("nothing may be forwarded: %v", e.work.calls)
	}
}

// TestForwardMessagesAtLimitIsAllowed proves the cap is inclusive: exactly
// 50 messages pass validation and reach account resolution instead of
// being rejected as "above the limit".
func TestForwardMessagesAtLimitIsAllowed(t *testing.T) {
	e := forwardFixture(t)
	items := make([]ForwardItem, 50)
	for i := range items {
		items[i] = ForwardItem{MessageID: fmt.Sprintf("m-%d", i)}
	}
	_, _, err := e.h.forwardMessages(context.Background(), nil, ForwardMessagesInput{
		Messages: items,
		To:       []string{"d@example.com"},
	})
	if err != nil && strings.Contains(err.Error(), "above the limit") {
		t.Fatalf("50 items must be allowed: %v", err)
	}
	if len(e.asked) == 0 {
		t.Fatal("50 items must pass validation and reach account resolution")
	}
}

// TestForwardMessagesCcDedupe proves the shared cc drops the account's own
// address, the to recipients and repeats, once for the whole batch.
func TestForwardMessagesCcDedupe(t *testing.T) {
	e := forwardFixture(t)
	e.work.headersByID = headersFor(e.work.headers, "m-1", "m-2")
	_, out, err := e.h.forwardMessages(context.Background(), nil, ForwardMessagesInput{
		Messages: []ForwardItem{{MessageID: "m-1"}, {MessageID: "m-2"}},
		To:       []string{"dave@example.com"},
		Cc:       []string{"Dave <dave@example.com>", "control@example.com", "erin@example.com", "erin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Cc, ",") != "<erin@example.com>" {
		t.Fatalf("cc = %v (to, self and duplicates must be dropped)", out.Cc)
	}
	hdr, _ := parseUpload(t, e.work.uploads[1])
	if hdr.Get("Cc") != "<erin@example.com>" {
		t.Fatalf("second mail Cc = %q", hdr.Get("Cc"))
	}
}
