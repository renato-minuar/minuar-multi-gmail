# Secret store per system

Date: 2026-09-30. Status: approved in chat, ready for a plan.

## 1. Goal

The binary runs on macOS, Linux desktops and Linux servers without a
library dependency. Windows compiles and uses the file store, untested.
At `setup` the binary probes the system, picks the best secret store it
finds, tells the user which one and why, and records the choice. Every
later command uses the recorded store and fails with a clear message when
it cannot reach it.

## 2. Non-goals

Windows Credential Manager. Any Go dependency beyond the current go.mod.
Moving secrets between stores. A passphrase-encrypted file.

## 3. Stores

| Kind | System | Mechanism |
|---|---|---|
| `keychain` | macOS | Current `keychainStore`, unchanged. |
| `secret-service` | Linux desktop | New `secretToolStore` in `internal/secrets/secrettool.go`. Shells out to `secret-tool`, the same shape as `keychain.go`: a `run func(args []string, stdin string) runResult` field so tests inject a fake. |
| `file` | Any system | New `fileStore` in `internal/secrets/file.go`. `secrets.json` in the config directory, mode 0600, a JSON object of name to secret. Writes are atomic: temp file in the same directory, chmod 0600, rename, the same as `config.Save`. |

`secret-tool` calls, with `<svc>` the service name and `<name>` the item:

- Get: `secret-tool lookup service <svc> account <name>`. Exit 0 with the
  secret on stdout (trailing newline trimmed). Exit 1 with empty stderr is
  `ErrNotFound`. Anything else is a failure with stderr in the message.
- Set: `secret-tool store --label=minuar-multi-gmail <name> service <svc> account <name>`
  with the secret on stdin, no trailing newline (secret-tool stores stdin verbatim). Then Get and compare, as
  `keychainStore.Set` does.
- Delete: Get first; `ErrNotFound` propagates. Then
  `secret-tool clear service <svc> account <name>`.

`ValidSecret` applies to every store.

## 4. Choosing the store

`secrets.Kind` is a string type with the three values above.
`config.File` gets `Secrets Kind` serialised as `"secrets,omitempty"`.

`secrets.Detect(service string) (Kind, string)` returns the kind the
system supports and one sentence for the user:

1. `MINUAR_MULTI_GMAIL_SECRETS` set: that kind, sentence "forced by the
   environment". An unknown value is an error at `setup`.
2. `runtime.GOOS == "darwin"`: `keychain`.
3. `runtime.GOOS == "windows"`: `file`.
4. Otherwise: `secret-service` when `secret-tool` is on PATH and a
   round-trip works: `Set("probe-<random>", value)`, `Get`, `Delete`.
   Otherwise `file`, with a sentence that says no keyring answered and
   names `secret-tool` (package `libsecret-tools` on Debian and Ubuntu,
   `libsecret` on Fedora and Arch) as the way to get one.

`setup` calls `Detect`, prints the sentence, and:

- `keychain`, `secret-service`, or `file` on Windows: stores the client
  credentials, writes `Secrets` into `accounts.json` (creating the file
  when missing), prints where the secrets went.
- `file` on Linux without the environment variable: prints the sentence
  and "Set MINUAR_MULTI_GMAIL_SECRETS=file to accept the file store, or
  install a keyring and run setup again", stores nothing, exits 1.

`secrets.Open(kind Kind, service, configDir string) (Store, error)`
builds the store. `keychain` on a system other than macOS and
`secret-service` on Windows are errors.

`openStore(configDir string) (secrets.Store, error)` in `cmd` reads the
recorded kind: environment variable first, then `accounts.json`. An empty
recorded kind is `keychain` on macOS (every existing install) and an
error "run minuar-multi-gmail setup first" elsewhere. Every command that
built `secrets.NewKeychain(secrets.ServiceName())` calls `openStore`
instead: `add-account`, `remove-account`, `doctor`, `serve`.

`serve` must not exit when the store cannot be opened, because Claude
Code would only show a dead server. It logs the error and serves with an
`errStore` whose three methods return that error, so every tool call
carries the message.

`doctor` prints a first line `secrets` with the kind in use, then the
existing checks. `setup` and `doctor` help texts say "secret store" instead
of "Keychain".

## 5. Browser

`openBrowser` in `cmd` picks the command by `runtime.GOOS`: `open` on
macOS, `rundll32 url.dll,FileProtocolHandler <url>` on Windows,
`xdg-open` elsewhere.

`googleauth.Login` no longer aborts when `OpenURL` fails. It writes
"Could not open a browser (<err>). Open the URL above yourself." to
`Notify` and keeps waiting for the callback. A test covers: `OpenURL`
returns an error, the callback arrives, the login succeeds.

## 6. Portability details

- File mode assertions in `internal/config/config_test.go` and
  `internal/tools/handlers_attachment_test.go` skip on Windows.
- Comments and help texts that say "Keychain" for the store in general say
  "secret store". The macOS store keeps its name.
- `scripts/install.sh` stays; `go build` is the same everywhere.

## 7. README

Replace "Why macOS only" with "Systems": one paragraph per store, the
acceptance step for the file store, and the Windows status (compiles, file
store, untested). Quick start: "You need macOS or Linux". The safety
section names the file store and its exposure: anyone with your user
account, or root, can read it.

## 8. Tests

| Package | Tests |
|---|---|
| `secrets` | `secretToolStore` through a fake runner: Set uses stdin and reads back, mismatch fails, exit 1 on lookup is `ErrNotFound`, other failures carry stderr, Delete of a missing item is `ErrNotFound`. `fileStore`: round trip, missing name, delete, file mode 0600 (skipped on Windows), corrupt file error, atomic write leaves no temp file. `Detect`: environment override, unknown value, and the Linux branch with an injected `lookPath` and probe store. `Open`: each kind, wrong system errors. |
| `googleauth` | Login continues after a failed `OpenURL`. |
| `cmd` | `openStore`: env, recorded kind, empty kind on darwin, empty kind elsewhere. `setup` on the file kind without acceptance exits 1 and stores nothing; with acceptance records `file`. `serve` with an unreachable store still lists tools and a tool call returns the store error. `doctor` prints the secrets line. |
| Linux | `go test -race ./...` in a Docker container. Then in the container: `setup` without a keyring exits 1 with the sentence; with `MINUAR_MULTI_GMAIL_SECRETS=file` it records `file`, and `doctor` reports it. The `secret-service` kind is verified in the container with `dbus-run-session` and `gnome-keyring-daemon` when that works; otherwise it is reported as verified through the fake runner only. |
