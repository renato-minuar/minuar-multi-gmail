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
- "What is the total on the invoice attached to that mail?" Claude saves the
  PDF to a private folder on your machine and reads it.
- "Archive everything from that newsletter" or "mark the thread read".

It runs on your own machine (macOS or Linux), talks only to Google, and keeps
every secret in the system keyring, or in a private file on machines without
one. Nothing is hosted anywhere and no
credentials ship with the code.

## How it keeps you safe

- Claude cannot send mail quietly. The only tools that send are
  `send_draft`, `forward_message` and `forward_messages`, and each one makes
  Claude Code ask you for approval on every call, in every permission mode.
  The prompt shows the recipient before anything leaves.
- Replies and new mail are drafts first. You can read them in Gmail before
  they go.
- There is no permanent delete. Trash is reversible.
- Attachments are saved only for a fixed list of file types, under
  `~/Library/Caches/minuar-multi-gmail/attachments` on macOS and
  `~/.cache/minuar-multi-gmail/attachments` on Linux (`$XDG_CACHE_HOME` when
  set), in a directory only you can read. The name a sender gave a file cannot move it anywhere else.
- Tokens and the OAuth client live in the macOS Keychain or the Linux Secret
  Service. On a machine without a keyring, `setup` offers a file store and
  stores nothing until you accept it with `MINUAR_MULTI_GMAIL_SECRETS=file`;
  that file is readable by anyone with your user account or root, and a
  dotfiles repo that tracks `~/.config` would commit `secrets.json`. The config
  file holds only aliases, addresses, the sentences you wrote, and which
  store is in use.

## Quick start

You need macOS or Linux, Go 1.26 and about ten minutes, most of them in the
Google Cloud console.

    git clone https://github.com/renato-minuar/minuar-multi-gmail.git
    cd minuar-multi-gmail
    scripts/install.sh

The script builds the binary into `~/.local/bin` and starts the wizard. The
wizard registers the server in Claude Code, opens the four Google console
pages you need one at a time (create a project, enable the Gmail API,
publish the consent screen, create a Desktop app client and download its
JSON), picks the downloaded file up from `~/Downloads` by itself, then logs
your accounts in and proposes an alias and a sentence for each one. Enter
accepts a proposal. On a Linux machine without a keyring it asks once
whether tokens may go to a private file. Running `minuar-multi-gmail` with no
arguments in a terminal also starts the wizard.

When it ends, restart Claude Code and ask it something about your mail. Run
`minuar-multi-gmail wizard` again at any time: every step skips what is
already done, so it is also the way to add an account later.

### By hand

The same steps as commands, for people who prefer them:

1. In the Google Cloud console create a project, enable the Gmail API, set
   the consent screen to External and publish it, create an OAuth client of
   type Desktop app and download its JSON.
2. `scripts/install.sh` (without a terminal it only builds) and
   `claude mcp add --scope user gmail -- ~/.local/bin/minuar-multi-gmail serve`.
3. `minuar-multi-gmail setup ~/Downloads/client_secret_1234.json`, then
   delete the file. On Linux without a keyring, prefix the command with
   `MINUAR_MULTI_GMAIL_SECRETS=file` to accept the file store.
4. `minuar-multi-gmail add-account work`, answer the description question
   with one plain sentence such as `the company mailbox; use it by default`.
   The first account becomes the default.
5. Repeat for the other accounts, then restart Claude Code.

## Everyday commands

| Command | What it does |
|---------|--------------|
| `minuar-multi-gmail wizard` | Set everything up, or add an account later. Skips what is done. |
| `minuar-multi-gmail accounts` | Show every alias, its address, which one is the default, and its description. |
| `minuar-multi-gmail set-description personal "…"` | Change the sentence for an account. `--clear` removes it. Restart Claude Code afterwards. |
| `minuar-multi-gmail set-default personal` | Change the default account. |
| `minuar-multi-gmail add-account work --replace` | Log an account in again, for example after Google asks for a fresh consent. Keeps the description. |
| `minuar-multi-gmail remove-account shop` | Revoke the token at Google, delete it from the secret store, forget the alias. |
| `minuar-multi-gmail doctor` | Check the secret store, the config and every token in one go. |

Less common: `setup <client_secret.json>` (step 3 above), `version`, and
`serve`, which Claude Code runs for you.

The config file is `~/.config/minuar-multi-gmail/accounts.json`. Keychain
items sit under the service `com.minuar.multi-gmail`. `secrets.json` next to
it exists only with the file store.

## What Claude can do with it

Every tool takes an optional `account` argument. Leave it out and the
default account is used. Every answer names the alias and address it used,
so you can always tell which mailbox Claude touched.

| Tool | What it does |
|------|--------------|
| `accounts_list` | Lists the aliases, addresses and the default. |
| `search_threads` | Searches with the same syntax as the Gmail search box: `from:`, `newer_than:7d`, `has:attachment`, `label:`, and so on. |
| `get_thread`, `get_message` | Read mail as plain text, with attachment names. |
| `get_attachment` | Saves the attachments of a message to files on your machine so Claude can open them. Only file types Claude can read are saved: PDF, images, text, and Word, Excel and PowerPoint files. |
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

## Systems

| System | Secret store | Login |
|--------|--------------|-------|
| macOS | Keychain, through the `security` command. | `open` starts the browser. |
| Linux desktop | Secret Service (GNOME Keyring, KWallet) through `secret-tool`. Install `libsecret-tools` (Debian, Ubuntu) or `libsecret` (Fedora, Arch). | `xdg-open` starts the browser. |
| Linux server | No keyring answers, so `setup` offers `secrets.json` in the config directory, mode 0600. Accept it with `MINUAR_MULTI_GMAIL_SECRETS=file` on the `setup` call. The wizard asks the same question with a y/N prompt instead. | No browser. `add-account` prints the login URL and the port it listens on. Either forward that port from your own machine (`ssh -L <port>:127.0.0.1:<port> <server>`) and open the URL there, or open the URL anywhere, let the final redirect to 127.0.0.1 fail, and run `curl` with that failed address on the server. |
| Windows | Compiles and uses `secrets.json`. Not tested. | `rundll32` starts the browser. |

`setup` probes the system once, says which store it chose and why, and
records the choice in `accounts.json`. Every later command uses that store
and reports when it cannot reach it. `MINUAR_MULTI_GMAIL_SECRETS` forces a
store for every command.

## For developers

    scripts/verify.sh                                   # gofmt, vet, build, tests with the race detector
    MINUAR_MULTI_GMAIL_KEYCHAIN_TEST=1 go test ./internal/secrets/ -run Integration -v   # touches the real Keychain with a throwaway item

The design specs live in `docs/superpowers/specs/`. Rebuild with
`scripts/install.sh` after changes; Claude Code picks the new binary up at
its next session start.

MIT licensed.
