# minuar-multi-gmail

Give Claude Code your Gmail. Several accounts, one server, and Claude picks
the right mailbox from the way you talk.

Once it is set up you can say things like:

- "What did the landlord write last week?" and Claude searches the mailbox
  you described as the one for the house.
- "Forward the three invoices from last month to my accountant." Claude finds
  them, shows you the list, and sends after you say yes.
- "Draft a reply to Ann saying Thursday works." A draft appears in Gmail;
  nothing goes out until you ask.
- "Archive everything from that newsletter" or "mark the thread read".

It runs on your Mac, talks only to Google, and keeps every secret in the
macOS Keychain. Nothing is hosted anywhere and no credentials ship with the
code.

## How it keeps you safe

- Claude cannot send mail quietly. The only tools that send are
  `send_draft`, `forward_message` and `forward_messages`, and each one makes
  Claude Code ask you for approval on every call, in every permission mode.
  The prompt shows the recipient before anything leaves.
- Replies and new mail are drafts first. You can read them in Gmail before
  they go.
- There is no permanent delete. Trash is reversible.
- Attachments are saved only for a fixed list of file types, under
  `~/Library/Caches/minuar-multi-gmail/attachments`, in a directory only
  you can read. The name a sender gave a file cannot move it anywhere else.
- Tokens and the OAuth client live in the Keychain. The config file holds
  only aliases, addresses and the sentences you wrote.

## Quick start

You need macOS, Go 1.26 and about fifteen minutes, most of it in the Google
Cloud console.

**1. Get an OAuth client from Google.** In the Google Cloud console create a
project, enable the Gmail API, set up the consent screen (external, publish
it, scope `gmail.modify`), and create an OAuth client of type **Desktop
app**. Download its JSON file. The design spec under `docs/` walks through
every click.

**2. Build and register the server.**

    scripts/install.sh
    claude mcp add --scope user gmail -- ~/.local/bin/minuar-multi-gmail serve

**3. Store the client.**

    minuar-multi-gmail setup ~/Downloads/client_secret_1234.json

Then delete the downloaded file; the Keychain has it now.

**4. Add your first account.**

    minuar-multi-gmail add-account work

A browser window opens for the Google login. Back in the terminal the tool
asks one question: when should Claude use this account? Answer in one plain
sentence, the way you would tell a colleague:

    the company mailbox; use it by default

That sentence is what Claude reads to choose between your accounts, so
mention the words you actually use. The first account you add becomes the
default.

**5. Add the others.**

    minuar-multi-gmail add-account personal
    minuar-multi-gmail add-account house

with descriptions such as `my personal gmail; use it when I say personal or
private` or `the inbox for the two rental houses; use it when I mention the
house, the lease or the landlord`.

**6. Restart Claude Code** and ask it something about your mail.

## Everyday commands

| Command | What it does |
|---------|--------------|
| `minuar-multi-gmail accounts` | Show every alias, its address, which one is the default, and its description. |
| `minuar-multi-gmail set-description personal "…"` | Change the sentence for an account. `--clear` removes it. Restart Claude Code afterwards. |
| `minuar-multi-gmail set-default personal` | Change the default account. |
| `minuar-multi-gmail add-account work --replace` | Log an account in again, for example after Google asks for a fresh consent. Keeps the description. |
| `minuar-multi-gmail remove-account shop` | Revoke the token at Google, delete it from the Keychain, forget the alias. |
| `minuar-multi-gmail doctor` | Check the Keychain, the config and every token in one go. |

Less common: `setup <client_secret.json>` (step 3 above), `version`, and
`serve`, which Claude Code runs for you.

The config file is `~/.config/minuar-multi-gmail/accounts.json`. Keychain
items sit under the service `com.minuar.multi-gmail`.

## What Claude can do with it

Every tool takes an optional `account` argument. Leave it out and the
default account is used. Every answer names the alias and address it used,
so you can always tell which mailbox Claude touched.

| Tool | What it does |
|------|--------------|
| `accounts_list` | Lists the aliases, addresses and the default. |
| `search_threads` | Searches with the same syntax as the Gmail search box: `from:`, `newer_than:7d`, `has:attachment`, `label:`, and so on. |
| `get_thread`, `get_message` | Read mail as plain text, with attachment names. |
| `get_attachment` | Saves the attachments of a message to files on your Mac so Claude can open them. Only file types Claude can read are saved: PDF, images, text, and Word, Excel and PowerPoint files. |
| `list_labels`, `modify_labels` | See and change labels. Removing `UNREAD` marks read, removing `INBOX` archives. Labels are never created. |
| `create_draft` | Write a new mail or a reply as a draft. Never sends. |
| `send_draft` | Send an existing draft. Asks you first. |
| `forward_message` | Forward one message with its attachments. Sends after you approve; `draft_only` keeps a draft; `as_eml` attaches the original as a `.eml` file when you ask for that. |
| `forward_messages` | Forward several messages to the same people in one call and one approval, with a per-message report. |
| `delete_draft` | Delete a draft. |
| `trash_thread`, `untrash_thread` | Move a conversation to the trash and back. |

## When something breaks

- **A tool says an account needs re-login.** Run `minuar-multi-gmail
  add-account <alias> --replace`, then restart Claude Code. Google does
  this when a token expires or when you revoke access.
- **Claude picks the wrong mailbox.** Sharpen the description with
  `set-description` and restart Claude Code. The sentence is all Claude has
  to go on.
- **Not sure what state things are in.** `minuar-multi-gmail doctor` reports
  on every piece.
- **The Google login shows "Access blocked".** Your OAuth consent screen is
  not published, or a Workspace admin has to allow the app. Both are fixed
  in the Google Cloud console, not here.

## Why macOS only

Two things: secrets are stored through the macOS `security` command, and
the login step opens the browser with `open`. Everything else is plain Go.
A second secrets backend behind the existing `Store` interface would make
it run on Linux.

## For developers

    scripts/verify.sh                                   # gofmt, vet, build, tests with the race detector
    MINUAR_MULTI_GMAIL_KEYCHAIN_TEST=1 go test ./internal/secrets/ -run Integration -v   # touches the real Keychain with a throwaway item

The design specs live in `docs/superpowers/specs/`. Rebuild with
`scripts/install.sh` after changes; Claude Code picks the new binary up at
its next session start.

MIT licensed.
