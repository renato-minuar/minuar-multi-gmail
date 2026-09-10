# forward_message: design

Extends `2026-09-03-multi-gmail-design.md`. That document's rules stay in
force; this one adds a tool and the pieces it needs.

## 1. Goal

Forward an existing message, with its attachments, from any configured
account. Two modes: inline (the Gmail way) and eml (the original attached
whole). The tool creates the draft and sends it unless `draft_only`.

## 2. Decisions taken

| Key | Decision |
|-----|----------|
| f1 | One tool, `forward_message`. It creates the draft and sends it unless `draft_only`. |
| f2 | Inline mode is the default. Eml mode is chosen by the `as_eml` input, which Claude sets only when the user says "eml" or "forward as attachment". The user never types a flag. |
| f3 | Inline mode re-attaches every part of the original that has a filename. Parts without a filename (inline signature images, `cid:` references) are dropped. There is no per-file selection in v1. |
| f4 | The draft stays in the original conversation: it carries the original thread id and the `In-Reply-To` and `References` headers, reusing the reply path. |
| f5 | The total attachment size is capped at 100 MB. Above the cap the tool fails before downloading anything. Google applies its own smaller limit on the upload and on send; that rejection is mapped to a plain error and leaves no draft. |
| f6 | Drafts that carry attachments are created through the Gmail media upload path (`message/rfc822`), not the JSON `raw` field. `create_draft` keeps using the JSON path unchanged. |

## 3. Non-goals

Per-attachment selection. Forwarding a whole thread. HTML bodies. Editing
the forwarded text. Forwarding from one account to be sent by another
(the draft is always created in the account that holds the original).

## 4. Tool contract

### Input

| Field | Required | Meaning |
|-------|----------|---------|
| `account` | no | Alias, same rule and description text as every other tool. |
| `message_id` | yes | Message id from `get_thread` or `get_message`. |
| `to[]` | yes | Recipients. |
| `cc[]`, `bcc[]` | no | Extra recipients. Deduplicated against `to` and self with `mime.DedupeCc`. |
| `body_text` | no | Note placed above the forwarded content. Empty allowed. |
| `subject` | no | Default: `Fwd: ` plus the original subject. If the original already starts with `fwd:` or `fw:` in any letter case, it is used as is. |
| `as_eml` | no | Default false. Description: "Attach the original as a .eml file instead of quoting it. Set only when the user asks for eml or to forward as attachment." |
| `draft_only` | no | Default false: the forward is sent. Set true only when the user asks for a draft or wants to review before sending. |

### Output

`CreateDraftOutput` fields (`draft_id`, `message_id`, `thread_id`, `from`,
`to[]`, `cc[]`, `bcc[]`, `subject`) plus:

| Field | Meaning |
|-------|---------|
| `sent` | Whether the forward was sent. `message_id` and `thread_id` are the sent message's ids when `sent`, the draft's when kept. |
| `mode` | `inline` or `eml`. |
| `forwarded_message_id` | The original message id. |
| `attachments[]` | `filename`, `mime_type`, `size_bytes` of every part attached to the draft. In eml mode this is the single `.eml` entry. Never nil. |

`draft_id` is present only when the draft is kept (`draft_only`). When the draft is created but the send fails, the tool returns an error naming the draft id so the caller can retry with `send_draft`.

### Validation order

1. `message_id` non-empty, `to` non-empty, all addresses parse.
2. Resolve account.
3. Fetch the original (section 5 or 6). Not found maps to the existing `NotFoundError`.
4. Size check (f5) before any attachment download.
5. Build, upload, log, return.

## 5. Inline mode

Data flow:

1. `Service.GetMessage(ctx, id, 0)`: full payload, no body truncation
   (`maxBodyChars` 0 means no limit in `truncateRunes`). This yields the
   headers, `BodyText` via the existing extraction (plain part, else HTML
   converted to text, else empty), and the attachment list with ids and
   sizes. A part whose bytes Gmail returns inline (no attachment id) is
   carried in `gmail.Attachment.Data` and attached without a download.
2. `Service.GetMessageHeaders(ctx, id)`: `RFCMessageID`, `References`,
   `Subject` and `ThreadID` for threading and the default subject. Two API
   calls per forward is accepted; `gmail.Attachment` gains `Data` for inline
   parts and `MessageHeaders` gains `SizeEstimate`; `gmail.Message` is
   otherwise unchanged.
3. Sum `SizeBytes` over attachments. Over 100 MB: error
   `attachments total N bytes, above the 100 MB limit`, nothing downloaded.
4. For each attachment: new `Service.GetAttachment(ctx, messageID,
   attachmentID) ([]byte, error)` wrapping `Users.Messages.Attachments.Get`
   and decoding the base64url `Data`. Same retry and error mapping as the
   other calls.
5. Body text, exactly:

```
<body_text, if non-empty, followed by one blank line>
---------- Forwarded message ---------
From: <original From header>
Date: <original Date, RFC 3339 in local time, as rendered by formatDate>
Subject: <original Subject>
To: <original To, comma separated>
Cc: <original Cc, comma separated; line omitted when empty>

<original BodyText>
```

   The separator line matches Gmail's own so recipients' clients collapse
   it. An empty original body still emits the block with an empty tail.
6. `mime.Build` with `Attachments` (section 8), `InReplyTo` and
   `References` from step 2, then `Service.CreateDraftUpload` (section 9)
   with the original thread id.

## 6. Eml mode

1. `Service.GetMessageHeaders(ctx, id)` as in inline mode.
2. Check `SizeEstimate` from the headers call against the cap before the raw
   download; then new `Service.GetRawMessage(ctx, id) ([]byte, error)`
   wrapping `Users.Messages.Get(...).Format("raw")`, which returns the
   decoded RFC 5322 bytes, and check the exact length again against the same
   100 MB cap.
3. Body text: `body_text`, or `Forwarded message attached.` when empty.
4. One attachment: content type `message/rfc822`, the raw bytes unmodified,
   filename from the original subject: control characters and every one of
   `/ \ : ? * < > | "` replaced by `_`, trimmed, cut to 100 runes, `.eml`
   appended. Empty subject gives `forwarded-message.eml`.
5. Threading headers and thread id as in inline mode.

## 7. Subject rule

`mime.ForwardSubject(orig string) string`: returns `orig` unchanged when
its trimmed lower-case form starts with `fwd:` or `fw:`; else `"Fwd: " +
orig`. An explicit `subject` input wins and is used verbatim (after the
existing header line-break check).

## 8. MIME builder

`mime.Message` gains:

```go
Attachments []Attachment // nil or empty: output unchanged from today

type Attachment struct {
    Filename    string
    ContentType string // default application/octet-stream
    Data        []byte
}
```

Rules:

- No attachments: the output is byte for byte what `Build` produces today.
  The existing tests are the guard.
- With attachments: top-level `Content-Type: multipart/mixed;
  boundary="..."`. Part one is the text part with the same headers the
  single-part form uses (`text/plain; charset=utf-8`, base64). Then one
  part per attachment with `Content-Type: <type>; name="<encoded>"`,
  `Content-Disposition: attachment; filename="<encoded>"`,
  `Content-Transfer-Encoding: base64`, body wrapped at 76 columns.
- The `name` and `filename` parameters are produced by
  `mime.FormatMediaType`, which quotes ASCII values and switches to the
  RFC 2231 `filename*=utf-8''...` form for other UTF-8 names. The boundary
  comes from `multipart.Writer`, which also writes the parts; the builder
  does not hand-roll boundaries.
- An attachment with an empty filename or nil data is an error from
  `Build`. A `message/rfc822` part is written with `Content-Transfer-Encoding:
  8bit` and its raw bytes, as RFC 2046 section 5.2.1 requires; every other
  part is base64.
- `Build` inspects attachment bytes only to see whether a `message/rfc822`
  part already ends with CRLF.

## 9. Gmail client additions

| Method | Wraps | Notes |
|--------|-------|-------|
| `GetAttachment(ctx, messageID, attachmentID) ([]byte, error)` | `Users.Messages.Attachments.Get` | Decodes `Data` with `base64.RawURLEncoding` (fallback to `URLEncoding` like `decodeBody`). |
| `GetRawMessage(ctx, messageID) ([]byte, error)` | `Users.Messages.Get` with `Format("raw")` | Decodes `Raw` with `base64.RawURLEncoding` (fallback to `URLEncoding`). |
| `CreateDraftUpload(ctx, raw []byte, threadID string) (*DraftResult, error)` | `Users.Drafts.Create(...).Media(bytes.NewReader(raw), googleapi.ContentType("message/rfc822"))` | The `Draft` metadata carries only `Message.ThreadId`; the body is the media. Same result mapping as `CreateDraft`. |

`Service` interface gains the three methods. The test fakes in
`internal/tools` implement them.

All three transfer methods run under `TransferTimeout` (5 minutes) instead of the 30 second `CallTimeout`, with the same single retry and error mapping. A draft upload near the 100 MB cap switches to a chunked resumable upload and cannot finish in 30 seconds.

## 10. Safety and audit

- Mail is sent by `send_draft` and `forward_message`. Both tools carry
  `_meta["anthropic/requiresUserInteraction"] = true`, so Claude Code
  prompts the user on every call in every permission mode (Claude Code
  2.1.199 or later).
- `forward_message` logs on stderr. Kept as a draft (`draft_only`): `forward
  draft created` with alias, draft id, mode, original message id, recipient
  count, attachment count and total bytes. Sent: `forward sent` with alias,
  draft id, sent message id, mode, original message id, the sent to/cc/subject,
  attachment count and total bytes. Never the body or the filenames' contents.
- The 100 MB cap applies before any download in both modes. A 413, or a
  Google error whose message contains "too large", on `CreateDraftUpload`
  maps to `gmail.TooLargeError`, text `message too large for Gmail (N
  bytes)`; the draft is not created.
- Attachment bytes live in memory for one call and are never written to
  disk.
- No new OAuth scope: `gmail.modify` already covers attachments and raw
  reads.

## 11. Testing

| Package | Cases |
|---------|-------|
| `mime` | `ForwardSubject`: plain, `Fwd:`, `FW:`, `fwd:` lower, empty. `Build` with one attachment, three, a UTF-8 filename, a `message/rfc822` part; parse the output back with `net/mail` and `mime/multipart` and check every part's headers and decoded bytes. `Build` with nil attachments equals the single-part output. Empty filename and nil data errors. |
| `gmail` | `httptest`: `GetAttachment` decodes base64url and maps 404. `GetRawMessage` decodes raw and maps 404. `CreateDraftUpload` sends a multipart upload whose media part is the raw message with `message/rfc822`, and the metadata carries the thread id; retry on 5xx; 413 maps to the too-large error. |
| `tools` | Fake service. Inline: body block layout with and without note, with and without Cc, attachments passed through in order, threading headers, thread id, default subject and explicit subject. Eml: single `.eml` attachment with raw bytes, filename sanitised, fallback filename, default body line. Size cap in both modes with zero download calls. Missing `message_id`, missing `to`, invalid address. Cc dedupe against `to` and self. |
| `cmd` (stdio) | The built binary lists `forward_message`, its schema requires exactly `message_id` and `to`, and the `as_eml` description is present. A call with a blank `message_id` (the SDK rejects an omitted required field at the schema layer) returns a tool error. The binary cannot reach a fake Gmail without Keychain credentials, so the upload body is covered by the `gmail` and `tools` tests. |

### Live acceptance

1. In work, find a mail with a PDF attachment. `forward_message` to
   personal with a note. Open the draft in Gmail: note on top, forwarded
   block, PDF attached, draft inside the original conversation.
2. `send_draft`. In personal, the mail arrives with the PDF openable.
3. Same message with `as_eml: true`. The draft carries one `.eml` file;
   opening it in Gmail shows the original with its own attachment.
4. A message whose attachments exceed 100 MB fails with the cap message and
   leaves no draft. A message between Google's limit and 100 MB fails with
   the too-large message and leaves no draft.

## 12. Documentation

README tool table gains `forward_message`. The server instructions in
`cmd/minuar-multi-gmail/serve.go` state that forwards are sent by
default, that `draft_only` keeps a draft instead, that every call asks
the user for approval, and that eml mode is used only on request.

## 13. forward_messages (added 2026-09-10)

`forward_message` carries `_meta["anthropic/requiresUserInteraction"]`, so
the client asks the user before every call. Forwarding twenty-four mails
costs twenty-four prompts. `forward_messages` forwards a list of messages
to the same recipients under one prompt: one call, one approval.

Input: `account`, `messages` (1 to 50 items, each `message_id` plus an
optional `subject`), `to`, `cc`, `bcc`, `body_text`, `as_eml`,
`draft_only`. The recipients, the note and the two mode flags are shared
by every message; only the message id varies. An item's `subject` is
informational: the client shows it in the approval prompt so the user sees
what is being forwarded. It never reaches the mail, whose subject follows
section 7 as in the single tool.

The 50 limit is a real cap, not a hint: the forwards run one after another
and each one downloads its attachments, so an unbounded list would hold an
approved call open for an unbounded time. Argument checks run before the
account is resolved and reject an empty list (`messages is required`), an
oversized list (`messages has N items, above the limit of 50`), a blank id
(`messages[i].message_id is required`, 0-based), a repeated id (`messages
has duplicate message_id X`) and an empty or invalid recipient list.

Behaviour: resolve the account once, dedupe the cc once against `to` and
the account's own address, then run the shared `forwardOne` core per
message in input order. A message that cannot be forwarded does not stop
the batch: its result carries `ok: false` and the error text, and the loop
moves to the next one. The tool returns an error only for the argument
checks above or a failed account resolution, so the caller always gets the
per-message picture. Once a failure exposes a cancelled context the loop
stops and the untouched messages are recorded with `skipped: <reason>`.

Output: `results` in input order, each with the message id, the echoed
subject, `ok`, `error`, `sent`, the draft or sent id, the thread id and the
attachment list (empty, never null); plus `sent_count`, `draft_count` and
`failed_count`. Logging keeps the per-message `forward sent` and `forward
draft created` lines from `forwardOne` and adds one `forward batch done`
line with the alias, mode, requested count, sent, drafts, failed and the
recipient count.

Testing: `tools` covers the sent batch (call sequence per message, one
upload each, counts, echoed subjects), a middle message that fails while
the others go out, `draft_only`, the argument table with the exact texts
and no service built, the cc dedupe applied once, and the skip path after
cancellation. The stdio test lists `forward_messages` with
`"required":["messages","to"]`, checks the approval flag on the three
sending tools and calls it with an empty list for the tool error.
