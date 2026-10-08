# Known bugs

Open items, newest first. Close an item by moving it to the bottom under "Fixed" with the commit.

## 1. Drafts open in Gmail with hard line breaks mid-sentence (08-10-2026)

**Seen:** a `create_draft` reply (work account, 08-10-2026) opened in Gmail's compose window with every paragraph broken into lines of about 65 to 70 characters, breaks falling mid-sentence. Renato: "the format is all broken".

**What the server does:** `internal/mime/build.go` `Build()` writes the body as a single `text/plain; charset=utf-8` part, base64 encoded (the 76-column split in `writeBase64` is on the base64 stream only, the decoded text keeps its original lines). Verified with a round trip the same day: a draft created with one 200-character paragraph came back from `get_message` with no line breaks inside the paragraph. So the stored body is intact.

**Cause:** Gmail opens a `text/plain`-only draft in its plain-text compose mode, which displays the text wrapped at a fixed column and, on send, converts it to hard-wrapped lines (RFC 3676 flowed text; many clients do not reflow it). A draft that carries an HTML alternative opens in rich-text mode and the paragraphs stay whole. The claude.ai Gmail connector, which accepts an `htmlBody`, does not show the problem on the same text.

**Fix:** build `multipart/alternative` in `Build()`: the existing `text/plain` part plus a `text/html` part generated from `BodyText` (escape HTML, one `<p>` per blank-line separated paragraph, `<br>` for single line breaks). Keep `body_text` as the only tool input so callers do not have to write HTML; an optional `body_html` input can come later. With attachments, nest the alternative inside the `multipart/mixed`. Applies to `create_draft`, `forward_message` and `forward_messages` alike.

**Repro:** `create_draft` with a 200-character paragraph, open the draft in Gmail's web compose, observe the wrapping. `get_message` on the same draft shows the paragraph unbroken.

## Fixed

(none yet)
