# minuar-multi-gmail

One local MCP server that gives Claude Code read and write access to
several Gmail accounts, one alias per account. You say "check my personal
mail" and Claude picks the right mailbox itself. macOS only: secrets live
in the Keychain.

## Requirements

- macOS, Go 1.26.
- Your own Google Cloud OAuth client of type Desktop app, with the Gmail
  API enabled and the consent screen published. This project ships no
  credentials and contacts no service other than Google: the client, the
  tokens and the mail stay yours.

## Install

    scripts/install.sh
    claude mcp add --scope user gmail -- ~/.local/bin/minuar-multi-gmail serve

Rebuild with `scripts/install.sh` after code changes. Claude Code picks the
new binary up at its next session start.

## Setup

1. `minuar-multi-gmail setup ~/Downloads/client_secret_1234.json` stores
   the OAuth client id and secret in the Keychain. Delete the downloaded
   file afterwards.
2. `minuar-multi-gmail add-account work` logs one Google account in through
   the browser, then asks when Claude should use it. Answer with one
   sentence, for example `the company mailbox; use it by default`. The
   first account added becomes the default.
3. Repeat step 2 per mailbox: `add-account personal`, `add-account shop`,
   and so on. One alias, one Google account.
4. `minuar-multi-gmail accounts` lists alias, address, the default mark and
   the description:

```
work         you@company.example            (default)  the company mailbox; use it by default
personal     you@gmail.com                             the personal mailbox; use it when they say personal or private
```

The description is the sentence Claude reads to choose an account, so write
it the way you talk: `the personal mailbox; use it when they say personal,
private or gmail`. Change it later with

    minuar-multi-gmail set-description personal "the personal mailbox; use it when they say personal or private"

Run `set-description personal --clear` to remove it. The server renders the descriptions into its
instructions and into every tool's `account` argument at startup, so start
a new Claude Code session after changing one.

## Commands

| Command | What it does |
|---------|--------------|
| `minuar-multi-gmail setup <client_secret.json>` | Store the OAuth client id and secret in the Keychain. Delete the file afterwards. |
| `minuar-multi-gmail add-account <alias> [--replace] [--description <text>]` | Log in one Google account in the browser and store its refresh token under the alias. Asks for the description unless `--description` gives it. First account becomes the default. |
| `minuar-multi-gmail set-description <alias> <text>` | Change the sentence that tells Claude when to use the account. Use `--clear` instead of `<text>` to remove it. |
| `minuar-multi-gmail accounts` | List aliases, addresses, the default and the descriptions. |
| `minuar-multi-gmail set-default <alias>` | Change the default account. |
| `minuar-multi-gmail remove-account <alias>` | Revoke the token at Google, delete it from the Keychain, forget the alias. |
| `minuar-multi-gmail doctor` | Refresh every token and confirm each belongs to the stored address. |
| `minuar-multi-gmail version` | Print the version. |
| `minuar-multi-gmail serve` | MCP server over stdio. Logs to stderr only. |

Config lives in `~/.config/minuar-multi-gmail/accounts.json` (aliases,
addresses and descriptions, never secrets). Keychain items sit under
service `com.minuar.multi-gmail`.

## Tools

`account` is optional on every tool and defaults to the configured default
alias. Its description lists every alias, its address and when to use it,
built from `accounts.json`. Every result names the alias and address it
used.

| Tool | Purpose |
|------|---------|
| `accounts_list` | Aliases, addresses, default. |
| `search_threads` | Gmail search syntax, compact rows, paging. |
| `get_thread` / `get_message` | Plain-text bodies, attachment names, truncation flag. |
| `list_labels` | System and user labels. |
| `modify_labels` | Add or remove labels by name or id. Remove `UNREAD` to mark read, remove `INBOX` to archive. Never creates labels. |
| `create_draft` | New message or reply (threading headers set). Nothing is sent. |
| `forward_message` | Forward a message with its attachments. Sends by default and asks for approval on every call; `draft_only` keeps a draft. Inline by default; `as_eml` attaches the original as a `.eml` file when asked. Stays in the original conversation. |
| `forward_messages` | Forward several messages to the same recipients in one call under one approval. Sends by default; `draft_only` keeps drafts. Reports per message what was sent or failed. |
| `send_draft` | Sends an existing draft. Asks for approval on every call. |
| `delete_draft` | Deletes a draft. |
| `trash_thread` / `untrash_thread` | Reversible trash. |

There is no permanent delete.

## Re-login

When a tool answers `account "work" (…) needs re-login`, run:

    minuar-multi-gmail add-account work --replace

The stored description survives the re-login. Then start a new Claude Code
session.

## Verify

    scripts/verify.sh                                   # gofmt, vet, build, tests
    MINUAR_MULTI_GMAIL_KEYCHAIN_TEST=1 go test ./internal/secrets/ -run Integration -v   # real Keychain, throwaway item
