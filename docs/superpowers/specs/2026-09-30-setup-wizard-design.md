# Setup wizard

Date: 2026-09-30. Status: approved in chat (option g1), ready for a plan.

## 1. Goal

A new user runs one command and ends with a working `gmail` server in
Claude Code. The wizard does every step a program can do: build, register
with Claude Code, choose the secret store, find and store the downloaded
OAuth client file, log accounts in, propose alias and description, set the
default, run the health check. The user clicks in the Google Cloud console,
logs in at Google, and presses Enter.

## 2. Non-goals

Creating the Google Cloud project, consent screen or OAuth client by API
(Google offers none). Shipping an OAuth client inside the binary. Prebuilt
binaries. Windows testing. Any change to the MCP tools.

## 3. Entry points

- `scripts/install.sh` builds the binary as today, then runs
  `"$HOME/.local/bin/minuar-multi-gmail" wizard` when stdin is a terminal.
  Without a terminal it prints the old registration hint.
- `minuar-multi-gmail wizard` runs the wizard.
- `minuar-multi-gmail` with no arguments runs the wizard when stdin is a
  terminal, else prints the usage as today (exit 2).
- The wizard refuses without a terminal: "the wizard needs a terminal; run
  it from your shell" (exit 2).

## 4. Dependencies (`wizardDeps`)

One struct in `cmd/minuar-multi-gmail/wizard.go`:

| Field | Production value | Purpose |
|---|---|---|
| `in io.Reader`, `out io.Writer` | stdin, stdout | prompts and answers |
| `configDir string` | `config.Dir()` | accounts.json |
| `downloads string` | `$HOME/Downloads` | where the console saves the client file |
| `binary string` | `os.Executable()` | the path registered in Claude Code |
| `goos string` | `runtime.GOOS` | store rules |
| `envKind string` | `os.Getenv(secrets.EnvKind)` | forced store |
| `detect func(service string) (secrets.Kind, string)` | `secrets.Detect` | store probe |
| `open func(kind secrets.Kind) (secrets.Store, error)` | `secrets.Open` | store |
| `lookPath func(string) (string, error)` | `exec.LookPath` | find `claude` |
| `run func(name string, args ...string) (string, error)` | `exec.Command(...).CombinedOutput` | `claude mcp get/add` |
| `openURL func(string) error` | `openBrowser` | console pages |
| `login func(ctx, creds) (*oauth2.Token, error)` | `googleauth.Login` with `openBrowser` and `out` | account login |
| `profile func(ctx, tok) (string, error)` | `gmail.ProfileEmail` | address after login |
| `doctorProfile func(ctx, ts) (string, error)` | `gmail.ProfileEmail` | doctor |
| `now func() time.Time`, `sleep func(time.Duration)` | `time.Now`, `time.Sleep` | watcher |
| `waitFile time.Duration` | 10 minutes | how long w3 watches Downloads |

`runWizard(ctx, d) error` runs the steps in order and stops at the first
error with the error printed and the hint "run minuar-multi-gmail wizard
again to continue".

## 5. Prompts

One helper `ask(d, question, def string) (string, error)` prints
`question [def]: `, reads one line, returns the trimmed line or `def` when
empty. `askYesNo(d, question string, def bool)` accepts y/yes/n/no in any
case, empty means `def`, anything else re-asks. EOF on stdin is an error
"input ended".

## 6. Steps

Every step prints a one-line heading (`== Secret store`), checks whether
its work is done, and says so in one line when it is.

### w1 Secret store

Reads `accounts.json`. Recorded kind present: print "Secrets go to <Describe>"
and continue. Otherwise call `detect`. Print the sentence. When the kind is
`file`, `goos` is not windows, `envKind` is empty: ask "Store tokens in a
private file, readable by your user account and root? [y/N]". No: return
the error "no secret store accepted; install a keyring (see the sentence
above) and run the wizard again". Yes: continue with `file` as accepted.
The chosen kind and the acceptance are kept in memory for w3; nothing is
written in w1 (setup records the kind when it stores the client).

### w2 Claude Code

`lookPath("claude")`. Missing: print "Claude Code is not on PATH. Register
later with: claude mcp add --scope user gmail -- <binary> serve" and
continue. Present: `run("claude", "mcp", "get", "gmail")`; exit 0 means
registered, print "Already registered in Claude Code" and continue. Else
`run("claude", "mcp", "add", "--scope", "user", "gmail", "--", binary,
"serve")`; failure prints the output and the manual command and continues
(registration is not fatal).

### w3 OAuth client

`googleauth.LoadClientCreds(store)` succeeds: print "OAuth client stored"
and continue. Otherwise, the guided console, four pages. For each page:
print the instruction and the URL, call `openURL`, then wait.

| Page | URL | Instruction |
|---|---|---|
| 1 | `https://console.cloud.google.com/projectcreate` | Create a project. Name: minuar-multi-gmail. Press Enter here when it exists. |
| 2 | `https://console.cloud.google.com/apis/library/gmail.googleapis.com` | Click Enable. Press Enter when done. |
| 3 | `https://console.cloud.google.com/auth/overview` | Configure the consent screen: app name minuar-multi-gmail, your email, audience External, then publish it (Audience page, "Publish app"). Press Enter when done. |
| 4 | `https://console.cloud.google.com/auth/clients/create` | Application type Desktop app, name minuar-multi-gmail, Create, then Download JSON. |

Pages 1-3 wait for Enter (empty line) on `in`. Page 4 waits for the file:
`watchDownloads(d, since time.Time) (string, error)` polls `downloads`
every second for a file matching `client_secret*.json` with modification
time after `since`, for `waitFile`, and returns its path. In parallel the
wizard reads a line from `in`: a non-empty line is a pasted path and wins;
an empty line re-prints "still waiting for the file in <downloads>; paste
its path here if it is elsewhere". Timeout returns the error "no client
file appeared in <downloads> within 10 minutes; download it and run the
wizard again".

With the path: call `storeClient(d.configDir, kind, envKind, goos, path,
out)`, the body of `runSetup` after detection, extracted so the wizard can
reuse it with the kind it already chose and the acceptance it already got
(the wizard passes `envKind = "file"` when the user said yes in w1). Then
ask "Delete the downloaded file? [Y/n]"; yes removes it.

The URLs are checked against the live console during the manual test of
this feature and corrected there.

### w4 Accounts

Loop. Question: "Add a Gmail account? [Y/n]" (the first round: "Add your
first Gmail account? [Y/n]"). No ends the loop; with zero accounts the
wizard prints "no account added; run the wizard again to add one" and
continues to w6.

Each round: `loginAccount(ctx, store, login, profile)` returns the token
and the address. Then the proposal:

- alias: `personal` for `gmail.com` and `googlemail.com`; `work` for the
  first other domain; for later domains the label before the top-level
  domain (`minuar.com` gives `minuar`, `mail.example.co.uk` gives
  `example`). When the proposal is taken, append `2`, `3`, ...
- description: `personal` gives "my personal gmail; use it when I say
  personal or private"; `work` gives "the mailbox at <domain>; use it by
  default and for anything about work"; others give "the mailbox at
  <domain>; use it when I mention <label>".

Ask "Alias [<proposal>]: " and "When should Claude use it? [<proposal>]: ".
An invalid alias or a duplicate re-asks with the reason. Then
`saveAccount(...)`: `cfg.Add`, store the refresh token, `config.Save`,
the same order and rollback as `runAddAccount`. Print "Connected <email>
as <alias>."

`runAddAccount` is split into `loginAccount` and `saveAccount` and keeps
its behaviour; its tests stay green.

### w5 Default

One account: nothing to ask (`cfg.Add` made it the default). Several and
the default is set: ask "Default account [<current>]: ", accept a known
alias, re-ask on an unknown one, and call `runSetDefault` when it changed.

### w6 Check

Run `runDoctor` with the wizard's store and `doctorProfile`. Print its
lines. Then: "Restart Claude Code, then ask it about your mail." Doctor
failure is reported, the wizard still exits 0 when the failure is only a
missing account.

## 7. README

Quick start becomes: install Go, clone, `scripts/install.sh`, follow the
wizard. The six manual steps stay under "By hand" for people who prefer
commands, shortened.

## 8. Tests

| Where | Tests |
|---|---|
| `wizard_test.go` | `ask`/`askYesNo` (default, yes, no, re-ask, EOF). `runWizard` with a scripted stdin and fakes: everything already done (all skips, no prompts); fresh run happy path on darwin (w2 registers, w3 pastes a path, w4 adds two accounts with proposed alias and description, w5 default, w6 doctor); Linux without keyring: "n" fails with the message and writes nothing, "y" continues with the file store; `claude` missing prints the manual command; `claude mcp get` exit 0 skips add; w3 file appears in a temp Downloads after a delay (sleep stubbed) and is deleted on "y"; w3 timeout error; w4 alias taken gets `2`; invalid alias re-asks; zero accounts message. |
| `addaccount` | `loginAccount`/`saveAccount` split keeps every existing `runAddAccount` test green. |
| `commands_test.go` | proposal functions: `proposeAlias(email, existing)`, `proposeDescription(alias, email)` table. |
| `serve_integration_test.go` or `main_test.go` | `minuar-multi-gmail` with no args and no terminal prints the usage, exit 2; `wizard` with no terminal prints the refusal, exit 2. |
| Manual | On this Mac with `MINUAR_MULTI_GMAIL_CONFIG_DIR` and `MINUAR_MULTI_GMAIL_KEYCHAIN_SERVICE` set to throwaway values: run the wizard end to end with the real console, one real login, then `doctor`; check the four URLs; then delete the throwaway Keychain items and directory. Renato's real config is not touched. |
