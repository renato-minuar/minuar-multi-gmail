# minuar-multi-gmail: design

Date: 2026-09-03
Status: design approved in chat; spec awaiting review

## 1. Goal

One local MCP server gives Claude Code read and write access to two Gmail
accounts from any project. The work account is the default. The personal
account is used when the user's wording points at it. Claude never asks
which account is meant.

## 2. Decisions taken

| Key | Decision |
|-----|----------|
| d1 | Full scope: OAuth scope `https://www.googleapis.com/auth/gmail.modify`. Read, search, labels, drafts, send. No permanent delete (the scope forbids it and no tool exists for it). |
| d2 | Google Cloud setup is driven by Claude in the user's logged-in Chrome. The user clicks every consequential step after Claude explains it. |
| d3 | Language: Go 1.26. Official MCP Go SDK `github.com/modelcontextprotocol/go-sdk` v1.7.x. Gmail client `google.golang.org/api/gmail/v1`. OAuth `golang.org/x/oauth2`. HTML to text `golang.org/x/net/html`. No other direct dependencies. |
| accounts | Aliases are the user's own. The examples in this document use `work` = you@company.example (default) and `personal` = you@gmail.com; a third alias holds a shared or project mailbox. |
| sending | Only `send_draft` sends mail. There is no `send_message` tool in v1. One send path, one MIME builder, and every outgoing mail exists as a draft first. |
| install | Binary at `~/.local/bin/minuar-multi-gmail`. Registered in Claude Code at user scope under the server name `gmail`. |

## 3. Non-goals for v1

Calendar and Drive. Attachment download. HTML composition. Message-level
trash (thread-level only). Permanent delete. Quoted-history stripping.
Linux and Windows. More than one operating-system user. Any transport other
than stdio.

## 4. Architecture

One Go binary, `minuar-multi-gmail`, with subcommands.

| Subcommand | Purpose |
|------------|---------|
| `setup <client_secret.json>` | Reads `installed.client_id` and `installed.client_secret` from the file Google Cloud downloads and stores both in the Keychain. Prints the `rm` command for the file. Does not delete the file itself. |
| `add-account <alias> [--replace]` | Runs the browser OAuth flow, stores the refresh token, records alias and email. Refuses an existing alias without `--replace`. Refuses an email already registered under another alias. |
| `remove-account <alias>` | Revokes the token at Google (best effort), deletes the Keychain item, removes the config entry. Clears the default if it pointed here. |
| `set-default <alias>` | Sets the default account. |
| `accounts` | Prints aliases, emails, and the default marker. |
| `doctor` | Checks Keychain items, parses the config, and for every account refreshes the token and confirms the profile email matches the stored one. Non-zero exit on any failure. |
| `serve` | Runs the MCP server over stdio. Logs to stderr only. |

Package layout:

| Package | Responsibility | Depends on |
|---------|----------------|------------|
| `cmd/minuar-multi-gmail` | Argument parsing, wiring, exit codes | all internal packages |
| `internal/config` | `accounts.json` read and write, validation, file permissions | none |
| `internal/secrets` | `Store` interface (`Get`, `Set`, `Delete`). `keychainStore` shells out to `security`. `memStore` for tests. | none |
| `internal/googleauth` | OAuth client config, PKCE loopback login, per-account token source that persists a rotated refresh token, error classification | `config`, `secrets` |
| `internal/gmail` | `Service` interface over the Gmail API (search, thread, message, labels, drafts, send, trash) and the real implementation. Body extraction, HTML to text, label name resolution, retries. | `googleauth` |
| `internal/mime` | RFC 5322 message builder: headers, RFC 2047 subject encoding, base64 body, reply headers | none |
| `internal/tools` | MCP tool registration. Typed argument structs, account resolution, compact JSON results, error text. | `gmail`, `config` |

Every package exposes one small interface and has its own tests. `tools`
tests use a fake `gmail.Service`. `gmail` tests use recorded JSON fixtures.
`googleauth` tests use `httptest` servers. `secrets` tests use `memStore`,
plus one Keychain integration test behind an environment flag.

## 5. Storage

### Keychain

Service name `com.minuar.multi-gmail`. Items:

| Account name | Content |
|--------------|---------|
| `oauth-client-id` | OAuth client ID |
| `oauth-client-secret` | OAuth client secret |
| `refresh-token.<alias>` | Refresh token for that alias |

Writes go through `security -i` with the `add-generic-password -U -s
<service> -a <name> -w <secret>` command on stdin, so no secret appears in
process arguments or `ps` output. Before a write the secret must match
`^[A-Za-z0-9._~/+=-]+$`; anything else is rejected with an error. That
charset needs no quoting in the `security` command parser. Reads use
`security find-generic-password -s <service> -a <name> -w`. Deletes use
`delete-generic-password`. The environment variable
`MINUAR_MULTI_GMAIL_KEYCHAIN_SERVICE` overrides the service name and exists for
the integration test only.

### Config file

Path `~/.config/minuar-multi-gmail/accounts.json`, directory mode 0700, file mode
0600, written atomically (temp file plus rename). Environment variable
`MINUAR_MULTI_GMAIL_CONFIG_DIR` overrides the directory and exists for tests.

```json
{
  "version": 1,
  "default": "work",
  "accounts": [
    { "alias": "work", "email": "you@company.example", "description": "the company mailbox; use it by default", "added_at": "2026-09-03T12:00:00Z" },
    { "alias": "personal", "email": "you@gmail.com", "description": "the personal mailbox; use it when they say personal or private", "added_at": "2026-09-03T12:05:00Z" }
  ]
}
```

Alias rule: `^[a-z][a-z0-9-]{0,31}$`. The file never contains a token or a
client secret.

### Per-account descriptions (added 2026-09-10)

`description` is optional and says, in one sentence, when Claude should use
that account. `add-account` asks for it after the login when stdin is a
terminal, or takes it from `--description <text>`; `set-description <alias>
<text>` edits it later and an empty text clears it. `--replace` keeps the
stored description when the answer is empty.

The server renders that text twice, once at startup:

- The `account` argument of every tool except `accounts_list` gets
  `Account alias. Default '<alias>' (<address>). '<alias>' (<address>):
  <description>. ... Pick the account from the user's wording and never ask
  which one.`
- The server instructions get `Gmail for <n> accounts. The default account
  is '<alias>' (<address>). ` followed by the same per-account sentences
  and the fixed rules about sending.

An account without a description renders `use it when the user names this
alias or address`. With no accounts configured both texts say so and point
at `add-account`. Because the rendering happens at startup, a changed
description reaches Claude at the next session. Nothing account-specific is
compiled into the binary: the struct tags carry a generic sentence that
points at the server instructions, used only when `accounts.json` cannot be
read.

## 6. Authentication

### Login flow (`add-account`)

1. Load client ID and secret from the Keychain. Fail with a pointer to
   `setup` if absent.
2. Listen on `127.0.0.1:0`. Redirect URL is
   `http://127.0.0.1:<port>/callback`.
3. Generate a 32-byte random `state` and a PKCE verifier
   (`oauth2.GenerateVerifier`).
4. Build the auth URL with scope `gmail.modify`, `access_type=offline`,
   `prompt=consent`, S256 challenge. Open it with `open`. Print it too.
5. The handler accepts exactly one request: it checks `state`, takes
   `code`, answers with a plain page saying the tab can be closed, and
   shuts the listener down. Any mismatch or a Google `error` parameter
   fails the login.
6. Exchange the code with `oauth2.VerifierOption`. Require a non-empty
   refresh token; otherwise fail and tell the user to remove the app at
   myaccount.google.com/permissions and retry.
7. Call `users.getProfile("me")` with the new token to learn the email.
8. Store the refresh token in the Keychain, then write the config entry.
   The first account ever added becomes the default; `set-default`
   changes it later.
9. Whole flow times out after 5 minutes and cleans up.

### Runtime token handling

Per alias, `oauth2.Config.TokenSource` wrapped in a persisting source: after
every `Token()` call the refresh token is compared with the stored one and
written back if Google rotated it. One `gmail.Service` per alias, created
on first use and cached behind a mutex. Every API call runs under a
30-second context timeout.

### Error classification

| Google response | Behaviour |
|-----------------|-----------|
| `invalid_grant` on refresh, or 401 after one refresh | Error: `account "work" (you@company.example) needs re-login: run minuar-multi-gmail add-account work --replace` |
| 403 `insufficientPermissions` | Same re-login message, with a note that the scope changed |
| 404 | Error: `not found: <id>` |
| 429 or 5xx | One retry after 1 second plus jitter, then the Google error message |
| Other 4xx | Google error message, no retry |

## 7. MCP tools

Server name in Claude Code: `gmail`. Tool names below appear as
`mcp__gmail__<name>`.

Every tool except `accounts_list` takes an optional `account` argument.
Omitted means the default account. Unknown alias returns an error listing
the known aliases. Its description is rendered from `accounts.json` at
startup (section 5). Every result includes the `account` alias and `email`
actually used.

| Tool | Arguments | Returns |
|------|-----------|---------|
| `accounts_list` | none | `default`, `accounts[]{alias,email}` |
| `search_threads` | `query` (Gmail search syntax, required), `max_results` 1-50 default 10, `page_token`, `include_spam_trash` default false | `threads[]{thread_id, message_count, last_message_at, from, subject, snippet, labels[], unread}`, `next_page_token` |
| `get_thread` | `thread_id` (required), `max_body_chars` 1-100000 default 5000 | `thread_id`, `messages[]` (message shape below) |
| `get_message` | `message_id` (required), `max_body_chars` | one message |
| `list_labels` | none | `labels[]{id,name,type}` |
| `modify_labels` | `thread_id` (required), `add_labels[]`, `remove_labels[]` | `thread_id`, `labels_after[]` |
| `create_draft` | `to[]`, `cc[]`, `bcc[]`, `subject`, `body_text` (required), `reply_to_message_id`, `reply_all` default false | `draft_id`, `message_id`, `thread_id`, `from`, `to[]`, `cc[]`, `bcc[]`, `subject` |
| `send_draft` | `draft_id` (required) | `message_id`, `thread_id`, `to[]`, `cc[]`, `subject` |
| `delete_draft` | `draft_id` (required) | `draft_id`, `deleted: true` |
| `trash_thread` | `thread_id` (required) | `thread_id`, `labels_after[]` |
| `untrash_thread` | `thread_id` (required) | `thread_id`, `labels_after[]` |

Message shape: `message_id`, `thread_id`, `date` (RFC 3339, local zone),
`from`, `to[]`, `cc[]`, `reply_to`, `subject`, `labels[]` (names),
`body_text`, `body_truncated`, `body_source` (`text/plain`, `text/html`,
or `none`), `attachments[]{filename, mime_type, size_bytes, attachment_id}`.

Results are returned as one JSON text content block, compact, with empty
fields omitted. The tool descriptions state that `send_draft` sends mail
that cannot be recalled, that `modify_labels` with `remove_labels:
["UNREAD"]` marks a thread read, and that `remove_labels: ["INBOX"]`
archives it.

### Label resolution

`add_labels` and `remove_labels` accept label names or IDs. System IDs
(`INBOX`, `UNREAD`, `STARRED`, `IMPORTANT`, `SPAM`, `TRASH`) pass through.
Other values are matched case-sensitively against the account's label list,
which is cached per alias and refreshed once on a miss. An unknown label is
an error that lists the available labels. Labels are never created.

### Search implementation

`threads.list` with `q`, `maxResults`, `pageToken`, `includeSpamTrash`,
then `threads.get(format=metadata, metadataHeaders=From,To,Subject,Date)`
for each thread, at most 5 in parallel. The last message in the thread
supplies `from`, `subject`, and `last_message_at` (from `internalDate`).
`unread` is true when any message carries `UNREAD`. `labels[]` is the union
of the messages' label IDs mapped to names.

## 8. Message rendering

Body extraction walks the payload tree. Preference: first `text/plain`
part; otherwise first `text/html` part converted to text; otherwise
`body_source: none`. Parts with a `filename` are listed as attachments and
never rendered. Bodies are decoded from base64url. HTML conversion uses the
`x/net/html` tokenizer: `script`, `style`, and `head` content dropped, block
elements and `br` become newlines, entities decoded, runs of more than two
newlines collapsed, leading and trailing whitespace trimmed. Truncation
cuts at `max_body_chars` on a rune boundary and sets `body_truncated`.
Header values come from the API already decoded.

## 9. Sending

### MIME builder

Headers: `From` (the account email), `To`, `Cc`, `Bcc`, `Subject`, `Date`,
`MIME-Version: 1.0`, `Content-Type: text/plain; charset=utf-8`,
`Content-Transfer-Encoding: base64`, and for replies `In-Reply-To` and
`References`. A non-ASCII subject is encoded with `mime.QEncoding` in
UTF-8. Recipients are parsed with `net/mail.ParseAddress`; an invalid
address is an error naming it. The body is base64 encoded in 76-column
lines. The whole message is base64url encoded into `Message.Raw`, with
`ThreadId` set for replies. Gmail honours the `Bcc` header in raw drafts
and strips it on send.

### New draft rules

`to` must have at least one address. `subject` is required.

### Reply rules (`reply_to_message_id` given)

1. Fetch the original with `format=metadata` and headers `Message-ID`,
   `References`, `Subject`, `From`, `To`, `Cc`, `Reply-To`.
2. `thread_id` is the original's. `In-Reply-To` is the original
   `Message-ID`. `References` is the original `References` plus its
   `Message-ID`.
3. `subject` defaults to the original subject prefixed with `Re: ` unless
   it already starts with `re:` case-insensitively.
4. `to` defaults to the original `Reply-To`, else `From`.
5. `reply_all` adds the original `To` and `Cc` addresses to `cc`, minus the
   account's own address and minus duplicates, compared case-insensitively.
6. At least one recipient must remain, otherwise an error.

### Send audit

`send_draft` logs, on stderr, the account alias, draft ID, resulting
message ID, recipients, and subject. `create_draft` logs alias, draft ID,
recipient count. No body text is ever logged.

## 10. Safety rules

1. Sending happens only through `send_draft` and `forward_message`, both
   of which prompt the user on every call. A reply never sends by itself.
   (amended 2026-09-09 by the forward_message spec)
2. `From` is always the account's own email. The builder has no `from`
   parameter.
3. No tool permanently deletes mail. `delete_draft` removes a draft only.
4. Secrets live in the Keychain only. They never appear in process
   arguments, the config file, logs, or tool results.
5. The MCP server writes nothing to stdout except protocol frames. All
   logging goes through `log/slog` to stderr.
6. The OAuth callback listener binds to `127.0.0.1` only, checks `state`,
   serves one request, and closes.
7. Every argument is validated before any network call: alias pattern,
   `max_results` and `max_body_chars` bounds, address syntax, non-empty
   body, label existence.
8. Config writes are atomic. A corrupt config file is an error that names
   the file; it is never overwritten silently.
9. Gmail API calls run under a 30-second timeout, at most 5 in parallel,
   with one retry on 429 and 5xx only.
10. `doctor` exists so a broken token is found by a command, not by a
    failed tool call in the middle of a task.

## 11. Testing

| Package | Tests |
|---------|-------|
| `mime` | New message and reply built, then parsed back with `net/mail`: headers, encoded non-ASCII subject, `References` chain, base64 body decodes to the input, `Bcc` present, invalid address rejected, `Re:` handling. |
| `gmail` | Body extraction fixtures: plain only, `multipart/alternative`, nested `multipart/mixed` with attachment, HTML only, empty body. Truncation on a rune boundary. HTML to text cases. Label resolution incl. system IDs, cache miss refresh, unknown name error. Retry on 429 and 5xx, no retry on 4xx, via `httptest`. |
| `googleauth` | Full loopback flow against an `httptest` OAuth server: state mismatch rejected, missing refresh token rejected, success stores token and email. Persisting token source writes a rotated refresh token. `invalid_grant` maps to the re-login error. |
| `secrets` | `memStore` behaviour. Charset rejection. Keychain integration test behind `MINUAR_MULTI_GMAIL_KEYCHAIN_TEST=1` using a throwaway service name, cleaned up in `t.Cleanup`. |
| `config` | Round trip, permissions 0700 and 0600, atomic write, corrupt file error, duplicate alias and duplicate email rejected, default handling. |
| `tools` | Every tool through a fake `gmail.Service`: default account resolution, explicit alias, unknown alias error, argument bounds, result shapes, reply recipient logic including `reply_all` self-exclusion. |
| `cmd` | Stdio integration: build the binary, connect with the SDK's `CommandTransport`, `tools/list` returns the 11 tools, `accounts_list` reads a temp config dir. |

`scripts/verify.sh` runs, in order: `gofmt -l` must print nothing, `go vet
./...`, `go build ./...`, `go test -race ./...`. It is the gate before any
claim of done.

### Live acceptance (after Google Cloud setup and both logins)

1. `minuar-multi-gmail doctor` reports both accounts healthy.
2. In a Claude Code session, `search_threads` without `account` returns
   work mail; with `account: personal` returns personal mail.
3. `create_draft` on work creates a draft that the user sees in Gmail.
4. `send_draft` sends that draft from work to personal. `search_threads`
   on personal finds it. This is the end-to-end proof.
5. `modify_labels` marks that thread read on personal; `trash_thread`
   then `untrash_thread` on it round-trip.
6. Output of every step is shown to the user.

## 12. Google Cloud setup procedure

Claude navigates and types. The user clicks each step marked (click) after
Claude explains it. Signed in as the Google account that should own the
project. A Workspace organisation may own it instead of the person.

| Step | Where | Action |
|------|-------|--------|
| g1 | console.cloud.google.com | Create project `multi-gmail`, the owning organisation or none, no folder. (click) |
| g2 | APIs & Services, Library | Enable Gmail API. (click) |
| g3 | Google Auth Platform, Branding | App name `multi-gmail`, support email and developer contact both the user's own address. |
| g4 | Google Auth Platform, Audience | User type External. Save. (click) |
| g5 | Google Auth Platform, Data Access | Add scope `.../auth/gmail.modify`. Update, Save. (click) |
| g6 | Google Auth Platform, Audience | Publish app, confirm. Status becomes In production, unverified. Refresh tokens no longer expire after 7 days. (click) |
| g7 | Google Auth Platform, Clients | Create client, type Desktop app, name `multi-gmail-mac`. Download JSON. (click) |
| g8 | Terminal | `minuar-multi-gmail setup ~/Downloads/client_secret_*.json`, then delete the file. |
| g9 | Terminal plus browser | `minuar-multi-gmail add-account work`. Google shows "Google hasn't verified this app": Advanced, Go to multi-gmail (unsafe), tick the Gmail permission, Continue. (click) |
| g10 | Terminal plus browser | `minuar-multi-gmail add-account personal`, same clicks with the personal account. (click) |

Contingencies:

- A Workspace login shows "Access blocked" from the organisation: Admin console,
  Security, Access and data control, API controls, Manage Third-Party App
  Access, Configure new app, search by the client ID, mark Trusted. Retry
  g9.
- Consent screen returns no refresh token: remove the app at
  myaccount.google.com/permissions for that account and retry.

## 13. Installation and registration

`scripts/install.sh` runs `go build -o ~/.local/bin/minuar-multi-gmail
./cmd/minuar-multi-gmail` and prints the registration command:

```
claude mcp add --scope user gmail -- ~/.local/bin/minuar-multi-gmail serve
```

Registration is done once. Rebuilding the binary is enough for code
changes; a running Claude Code session picks it up on its next start.
`claude mcp list` must show `gmail` as connected. The claude.ai Gmail
connector can stay enabled, but disconnecting it avoids two tool sets with
overlapping names.

## 14. Repository

Module path `github.com/renato-minuar/minuar-multi-gmail`. Local repository at
`~/projects/minuar-multi-gmail`, not pushed unless asked. Layout:

```
cmd/minuar-multi-gmail/
internal/{config,secrets,googleauth,gmail,mime,tools}/
scripts/{verify.sh,install.sh}
docs/superpowers/specs/
README.md
```

README covers setup, the subcommands, tool list, and the re-login
procedure.
