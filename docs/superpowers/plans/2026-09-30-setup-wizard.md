# Setup Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One command takes a new user from a fresh clone to a working `gmail` server in Claude Code, with the user only clicking in the Google console, logging in at Google, and pressing Enter.

**Architecture:** A `wizard` command in `cmd/minuar-multi-gmail` runs six step functions in order over one `wizardDeps` struct that carries every outside dependency (stdin/stdout, store, config dir, Downloads dir, `claude` lookup, command runner, browser opener, login). The steps reuse the existing command bodies: `runSetup`'s storing half is extracted as `storeClient`, `runAddAccount` is split into `loginAccount` and `saveAccount`, `runSetDefault` and `runDoctor` are called as they are. `scripts/install.sh` and a bare `minuar-multi-gmail` start the wizard when a terminal is present.

**Tech Stack:** Go 1.26 standard library only. Existing test style: `t.Fatalf` tests, fakes in structs, `t.TempDir()`, scripted stdin through `strings.NewReader`.

**Spec:** `docs/superpowers/specs/2026-09-30-setup-wizard-design.md`

## Global Constraints

- No new module in `go.mod`. `go.sum` stays as it is.
- The wizard refuses without a terminal: message "the wizard needs a terminal; run it from your shell", exit 2.
- Prompts: `question [default]: `; empty answer means the default; yes/no accepts y, yes, n, no in any case and re-asks on anything else; EOF on stdin is the error "input ended".
- Every step prints a heading line `== <name>` and, when its work is already done, one line saying so, then continues.
- Existing command behaviour stays: every existing test in `cmd/minuar-multi-gmail` keeps passing unchanged unless the plan names the change.
- Commit messages: imperative subject with a conventional prefix, under 72 characters, body as a separate paragraph on what changed and why. No co-author lines, no tool trailers, no model names. Stage by name.
- `scripts/verify.sh` (gofmt, vet, build, `go test -race ./...`) passes after every task.
- Console page URLs and instructions are exactly the spec §6 w3 table.

## Review Focus

1. A user runs the wizard twice with the client already stored and one account present: every step must skip in one line and the only prompt is "Add a Gmail account? [Y/n]" — pinned by the all-done test in Task 6.
2. A `client_secret*.json` that already sat in Downloads from an earlier attempt must not be picked up as if it were new (mtime before the wizard started), while a pasted path to that same file must work — pinned by the watcher tests in Task 5.
3. The login step opens a browser; when the user closes the terminal input (EOF) while the wizard waits for Enter on a console page, the wizard must stop with "input ended" and not hang — pinned by the EOF test in Task 1 and the page-wait test in Task 5.
4. A proposed alias that collides with an existing one gets a numeric suffix rather than an error loop — pinned in Task 2.
5. On a Linux machine without a keyring the user answers "n": nothing may be written anywhere (no `accounts.json`, no `secrets.json`, no Claude registration attempted after the refusal) — pinned in Task 4 and Task 6.

---

### Task 1: Prompt helpers, `wizardDeps`, the `wizard` command shell, and the no-argument entry

**Files:**
- Create: `cmd/minuar-multi-gmail/wizard.go`
- Create: `cmd/minuar-multi-gmail/wizard_test.go`
- Modify: `cmd/minuar-multi-gmail/main.go:53-56` (`run` with no args)
- Modify: `cmd/minuar-multi-gmail/main_test.go` (add one test)

**Interfaces:**
- Consumes: `stdinIsTerminal()` from `addaccount.go`, `commands`/`commandHelp` from `main.go`, `openBrowser` from `browser.go`, `secrets.Kind`, `secrets.Store`, `googleauth.ClientCreds`, `oauth2.Token`, `oauth2.TokenSource`.
- Produces:
  - `type wizardDeps struct` with the fields listed in Step 3.
  - `func (d *wizardDeps) lines() <-chan lineResult` — one background goroutine feeds stdin lines, unbuffered, closed at EOF.
  - `func (d *wizardDeps) readLine() (string, error)` — next line from `lines()`, trimmed of the newline; a closed channel is `errInputEnded`.
  - `var errInputEnded = errors.New("input ended")`.
  - `func ask(d *wizardDeps, question, def string) (string, error)`.
  - `func askYesNo(d *wizardDeps, question string, def bool) (bool, error)`.
  - `func heading(d *wizardDeps, name string)` — prints `== <name>`.
  - `func runWizard(ctx context.Context, d *wizardDeps) error` — in this task it only prints the headings placeholder-free: it returns `nil` after `heading(d, "Setup wizard")`. Task 6 fills it.
  - `func productionWizardDeps(io stdio) (*wizardDeps, error)` — builds the production struct; in this task it fills every field that needs no later task (in, out, configDir, downloads, binary, goos, envKind, detect, open, lookPath, run, openURL, login, profile, doctorProfile, now, sleep, waitFile).
  - The `wizard` command and the bare-invocation rule.

- [ ] **Step 1: Write the failing tests**

`cmd/minuar-multi-gmail/wizard_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// promptDeps is a wizardDeps with a scripted stdin and a captured stdout.
func promptDeps(input string) (*wizardDeps, *bytes.Buffer) {
	var out bytes.Buffer
	return &wizardDeps{in: strings.NewReader(input), out: &out}, &out
}

func TestAskReturnsDefaultOnEmptyLine(t *testing.T) {
	d, out := promptDeps("\n")
	got, err := ask(d, "Alias", "work")
	if err != nil || got != "work" {
		t.Fatalf("ask = %q, %v", got, err)
	}
	if out.String() != "Alias [work]: " {
		t.Fatalf("prompt = %q", out.String())
	}
}

func TestAskReturnsTypedAnswerTrimmed(t *testing.T) {
	d, _ := promptDeps("  house \n")
	got, err := ask(d, "Alias", "work")
	if err != nil || got != "house" {
		t.Fatalf("ask = %q, %v", got, err)
	}
}

func TestAskWithoutDefaultShowsNoBrackets(t *testing.T) {
	d, out := promptDeps("x\n")
	if _, err := ask(d, "Path", ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Path: " {
		t.Fatalf("prompt = %q", out.String())
	}
}

func TestAskEOFIsInputEnded(t *testing.T) {
	d, _ := promptDeps("")
	_, err := ask(d, "Alias", "work")
	if !errors.Is(err, errInputEnded) {
		t.Fatalf("err = %v, want errInputEnded", err)
	}
}

// A last line without a newline still counts as an answer.
func TestAskLastLineWithoutNewline(t *testing.T) {
	d, _ := promptDeps("house")
	got, err := ask(d, "Alias", "work")
	if err != nil || got != "house" {
		t.Fatalf("ask = %q, %v", got, err)
	}
}

func TestAskYesNo(t *testing.T) {
	cases := []struct {
		input string
		def   bool
		want  bool
	}{
		{"\n", true, true}, {"\n", false, false},
		{"y\n", false, true}, {"YES\n", false, true},
		{"n\n", true, false}, {"No\n", true, false},
		{"maybe\nyes\n", false, true},
	}
	for _, c := range cases {
		d, out := promptDeps(c.input)
		got, err := askYesNo(d, "Continue?", c.def)
		if err != nil || got != c.want {
			t.Fatalf("input %q def %v: got %v, %v", c.input, c.def, got, err)
		}
		wantPrompt := "Continue? [Y/n]: "
		if !c.def {
			wantPrompt = "Continue? [y/N]: "
		}
		if !strings.HasPrefix(out.String(), wantPrompt) {
			t.Fatalf("input %q: prompt = %q, want prefix %q", c.input, out.String(), wantPrompt)
		}
		if c.input == "maybe\nyes\n" && strings.Count(out.String(), "Continue?") != 2 {
			t.Fatalf("an unknown answer must re-ask: %q", out.String())
		}
	}
	d, _ := promptDeps("")
	if _, err := askYesNo(d, "Continue?", true); !errors.Is(err, errInputEnded) {
		t.Fatalf("EOF: err = %v", err)
	}
}

func TestHeading(t *testing.T) {
	d, out := promptDeps("")
	heading(d, "Secret store")
	if out.String() != "\n== Secret store\n" {
		t.Fatalf("heading = %q", out.String())
	}
}

func TestWizardCommandRefusesWithoutTerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"wizard"}, stdio{out: &out, err: &errOut})
	if code != 2 || !strings.Contains(errOut.String(), "the wizard needs a terminal; run it from your shell") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}
```

Add `"context"` to that file's imports. The `run` test relies on `stdinIsTerminal()` being false under `go test` (stdin is not a character device there), which is how `TestRunAddAccountWithoutPromptHintsAtSetDescription` already behaves.

Append to `cmd/minuar-multi-gmail/main_test.go`:

```go
// Without a terminal, a bare invocation prints the usage as before; with
// one it would start the wizard (not testable here).
func TestBareInvocationWithoutTerminalPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), nil, stdio{out: &out, err: &errOut})
	if code != 2 || !strings.Contains(errOut.String(), "Usage: minuar-multi-gmail <command>") || !strings.Contains(errOut.String(), "wizard") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}
```

Read `main_test.go` first and add whichever of `bytes`, `context`, `strings` it does not import yet.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /Users/benne-air/projects/minuar-multi-gmail && go test ./cmd/... 2>&1 | head`
Expected: build failure, `undefined: wizardDeps`, `undefined: ask`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/wizard.go`**

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// errInputEnded is returned when stdin closes while the wizard waits for
// an answer, so a closed terminal stops the wizard instead of hanging it.
var errInputEnded = errors.New("input ended")

// wizardDeps carries everything the wizard touches outside the process,
// so every step runs in tests with a scripted stdin and fakes.
type wizardDeps struct {
	in  io.Reader
	out io.Writer

	configDir string // accounts.json
	downloads string // where the browser saves the client file
	binary    string // the path registered in Claude Code
	goos      string
	envKind   string // MINUAR_MULTI_GMAIL_SECRETS

	detect   func(service string) (secrets.Kind, string)
	open     func(kind secrets.Kind) (secrets.Store, error)
	lookPath func(file string) (string, error)
	// run executes a command and returns its combined output; a non-zero
	// exit is the error.
	run     func(name string, args ...string) (string, error)
	openURL func(url string) error

	login         func(ctx context.Context, creds googleauth.ClientCreds) (*oauth2.Token, error)
	profile       func(ctx context.Context, tok *oauth2.Token) (string, error)
	doctorProfile func(ctx context.Context, ts oauth2.TokenSource) (string, error)

	now      func() time.Time
	sleep    func(time.Duration)
	waitFile time.Duration // how long w3 watches the Downloads directory

	lineCh chan lineResult // started on first use by lines()
}

type lineResult struct {
	line string
	err  error
}

// lines returns the channel one background goroutine feeds with stdin
// lines. The goroutine owns the reader for the wizard's whole life, reads
// at most one line ahead (the channel is unbuffered), and closes the
// channel at EOF. Any step can select on it without stealing a line from
// the next step: an unread line stays in the channel.
func (d *wizardDeps) lines() <-chan lineResult {
	if d.lineCh == nil {
		ch := make(chan lineResult)
		d.lineCh = ch
		go func() {
			r := bufio.NewReader(d.in)
			for {
				line, err := r.ReadString('\n')
				if line != "" {
					ch <- lineResult{line: strings.TrimRight(line, "\r\n")}
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						ch <- lineResult{err: fmt.Errorf("read input: %w", err)}
					}
					close(ch)
					return
				}
			}
		}()
	}
	return d.lineCh
}

// readLine returns the next stdin line without its newline. A closed
// input is errInputEnded.
func (d *wizardDeps) readLine() (string, error) {
	r, ok := <-d.lines()
	if !ok {
		return "", errInputEnded
	}
	return r.line, r.err
}

// ask prints "question [def]: " and returns the trimmed answer, or def
// when the answer is empty.
func ask(d *wizardDeps, question, def string) (string, error) {
	if def == "" {
		fmt.Fprintf(d.out, "%s: ", question)
	} else {
		fmt.Fprintf(d.out, "%s [%s]: ", question, def)
	}
	line, err := d.readLine()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// askYesNo accepts y, yes, n, no in any case; empty means def; anything
// else asks again.
func askYesNo(d *wizardDeps, question string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(d.out, "%s %s: ", question, hint)
		line, err := d.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(d.out, "Please answer y or n.")
	}
}

func heading(d *wizardDeps, name string) { fmt.Fprintf(d.out, "\n== %s\n", name) }

// productionWizardDeps wires the wizard to the real system.
func productionWizardDeps(io stdio) (*wizardDeps, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("own path: %w", err)
	}
	d := &wizardDeps{
		in: os.Stdin, out: io.out,
		configDir: dir, downloads: filepath.Join(home, "Downloads"), binary: binary,
		goos: runtime.GOOS, envKind: os.Getenv(secrets.EnvKind),
		detect: secrets.Detect,
		open: func(kind secrets.Kind) (secrets.Store, error) {
			return secrets.Open(kind, secrets.ServiceName(), dir)
		},
		lookPath: exec.LookPath,
		run: func(name string, args ...string) (string, error) {
			b, err := exec.Command(name, args...).CombinedOutput()
			return string(b), err
		},
		openURL: openBrowser,
		profile: func(ctx context.Context, tok *oauth2.Token) (string, error) {
			return gmail.ProfileEmail(ctx, option.WithTokenSource(oauth2.StaticTokenSource(tok)))
		},
		doctorProfile: func(ctx context.Context, ts oauth2.TokenSource) (string, error) {
			return gmail.ProfileEmail(ctx, option.WithTokenSource(ts))
		},
		now: time.Now, sleep: time.Sleep, waitFile: 10 * time.Minute,
	}
	d.login = func(ctx context.Context, creds googleauth.ClientCreds) (*oauth2.Token, error) {
		return googleauth.Login(ctx, googleauth.LoginOptions{Creds: creds, OpenURL: openBrowser, Notify: d.out})
	}
	return d, nil
}

// runWizard runs the steps in order. Task 6 adds the steps; until then it
// prints the heading and returns.
func runWizard(ctx context.Context, d *wizardDeps) error {
	heading(d, "Setup wizard")
	return nil
}

const wizardNeedsTerminal = "the wizard needs a terminal; run it from your shell"

func init() {
	commandHelp["wizard"] = "set everything up step by step: secret store, Claude Code, OAuth client, accounts"
	commands["wizard"] = func(ctx context.Context, _ []string, io stdio) int {
		if !stdinIsTerminal() {
			fmt.Fprintln(io.err, wizardNeedsTerminal)
			return 2
		}
		d, err := productionWizardDeps(io)
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		if err := runWizard(ctx, d); err != nil {
			fmt.Fprintln(io.err, "error:", err)
			fmt.Fprintln(io.err, "run minuar-multi-gmail wizard again to continue")
			return 1
		}
		return 0
	}
}
```

- [ ] **Step 4: Change `run` in `main.go` so a bare invocation starts the wizard on a terminal**

Replace

```go
	if len(args) == 0 {
		usage(io.err)
		return 2
	}
```

with

```go
	if len(args) == 0 {
		// A bare call from a shell is the first thing a new user tries.
		if stdinIsTerminal() {
			return commands["wizard"](ctx, nil, io)
		}
		usage(io.err)
		return 2
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`.

- [ ] **Step 6: Commit**

```bash
git add cmd/minuar-multi-gmail/wizard.go cmd/minuar-multi-gmail/wizard_test.go cmd/minuar-multi-gmail/main.go cmd/minuar-multi-gmail/main_test.go
git commit -m "feat(wizard): add the command shell, prompts and dependencies" -m "The wizard command exists, refuses without a terminal, and a bare invocation from a shell starts it. ask and askYesNo are the two prompt shapes every step uses; wizardDeps carries the outside world so steps run in tests with a scripted stdin."
```

---

### Task 2: Split `runAddAccount` into `loginAccount` and `saveAccount`; alias and description proposals

**Files:**
- Modify: `cmd/minuar-multi-gmail/addaccount.go:138-214` (`runAddAccount`)
- Create: `cmd/minuar-multi-gmail/wizard_propose.go`
- Create: `cmd/minuar-multi-gmail/wizard_propose_test.go`
- Modify: `cmd/minuar-multi-gmail/commands_test.go` (no change expected; all `runAddAccount` tests must still pass)

**Interfaces:**
- Consumes: `addAccountDeps`, `config.File`, `config.Account`, `config.ValidAlias`, `secrets.RefreshTokenKey`, `googleauth.LoadClientCreds`.
- Produces:
  - `func loginAccount(ctx context.Context, store secrets.Store, login func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error), profile func(context.Context, *oauth2.Token) (string, error)) (*oauth2.Token, string, error)` — loads the client, logs in, returns the token and the address.
  - `func saveAccount(configDir string, store secrets.Store, cfg *config.File, acct config.Account, tok *oauth2.Token, replace bool) error` — adds or replaces the account, stores the refresh token, saves the config, rolls the token back when the save fails.
  - `func proposeAlias(email string, taken func(alias string) bool) string`.
  - `func proposeDescription(alias, email string) string`.
  - `func emailDomain(email string) string` and `func domainLabel(domain string) string`.

- [ ] **Step 1: Write the failing tests for the proposals**

`cmd/minuar-multi-gmail/wizard_propose_test.go`:

```go
package main

import "testing"

func TestProposeAlias(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		email string
		taken map[string]bool
		want  string
	}{
		{"ann@gmail.com", nil, "personal"},
		{"Ann@GoogleMail.com", nil, "personal"},
		{"ann@minuar.com", nil, "work"},
		{"ann@minuar.com", map[string]bool{"work": true}, "minuar"},
		{"ann@mail.example.co.uk", map[string]bool{"work": true}, "example"},
		{"ann@minuar.com", map[string]bool{"work": true, "minuar": true}, "minuar2"},
		{"ann@minuar.com", map[string]bool{"work": true, "minuar": true, "minuar2": true}, "minuar3"},
		{"ann@gmail.com", map[string]bool{"personal": true}, "personal2"},
		{"ann@my-shop.example", map[string]bool{"work": true}, "my-shop"},
		{"ann@123.example", map[string]bool{"work": true}, "a123"},
		{"nonsense", nil, "account"},
	}
	for _, c := range cases {
		taken := none
		if c.taken != nil {
			taken = func(a string) bool { return c.taken[a] }
		}
		if got := proposeAlias(c.email, taken); got != c.want {
			t.Errorf("proposeAlias(%q, %v) = %q, want %q", c.email, c.taken, got, c.want)
		}
	}
}

func TestProposeDescription(t *testing.T) {
	cases := []struct{ alias, email, want string }{
		{"personal", "ann@gmail.com", "my personal gmail; use it when I say personal or private"},
		{"work", "ann@minuar.com", "the mailbox at minuar.com; use it by default and for anything about work"},
		{"minuar", "ann@minuar.com", "the mailbox at minuar.com; use it when I mention minuar"},
		{"house", "ann@gmail.com", "the mailbox at gmail.com; use it when I mention house"},
	}
	for _, c := range cases {
		if got := proposeDescription(c.alias, c.email); got != c.want {
			t.Errorf("proposeDescription(%q, %q) = %q, want %q", c.alias, c.email, got, c.want)
		}
	}
}

func TestEmailDomainAndLabel(t *testing.T) {
	if emailDomain("Ann@Minuar.COM") != "minuar.com" || emailDomain("nonsense") != "" {
		t.Fatal("emailDomain")
	}
	for domain, want := range map[string]string{"minuar.com": "minuar", "mail.example.co.uk": "example", "gmail.com": "gmail", "localhost": "localhost", "": ""} {
		if got := domainLabel(domain); got != want {
			t.Errorf("domainLabel(%q) = %q, want %q", domain, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run 'Propose|EmailDomain'`
Expected: build failure, `undefined: proposeAlias`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/wizard_propose.go`**

```go
package main

import (
	"fmt"
	"strings"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
)

// personalDomains are the consumer Gmail domains: their alias is
// "personal", every other domain is a work or organisation mailbox.
var personalDomains = map[string]bool{"gmail.com": true, "googlemail.com": true}

// emailDomain returns the lower-case domain of an address, or "".
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// publicSuffixes are the multi-label endings this tool recognises; the
// label before them names the organisation. Anything else uses the label
// before the last dot.
var publicSuffixes = map[string]bool{"co.uk": true, "com.au": true, "com.br": true, "co.nz": true, "org.uk": true, "ac.uk": true}

// domainLabel returns the organisation label of a domain: "minuar" for
// minuar.com, "example" for mail.example.co.uk.
func domainLabel(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return domain
	}
	if len(parts) >= 3 && publicSuffixes[strings.Join(parts[len(parts)-2:], ".")] {
		return parts[len(parts)-3]
	}
	return parts[len(parts)-2]
}

// proposeAlias suggests an alias for an address: "personal" for consumer
// Gmail, "work" for the first other domain, the domain label after that.
// A taken proposal gets 2, 3, ... appended.
func proposeAlias(email string, taken func(alias string) bool) string {
	domain := emailDomain(email)
	var base string
	switch {
	case domain == "":
		base = "account"
	case personalDomains[domain]:
		base = "personal"
	case !taken("work"):
		base = "work"
	default:
		base = domainLabel(domain)
	}
	if !config.ValidAlias(base) {
		// A label that starts with a digit or holds odd characters gets a
		// letter in front and the rest dropped.
		base = "a" + strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			return -1
		}, base)
		if !config.ValidAlias(base) {
			base = "account"
		}
	}
	candidate := base
	for n := 2; taken(candidate); n++ {
		candidate = fmt.Sprintf("%s%d", base, n)
	}
	return candidate
}

// proposeDescription suggests the sentence the model reads to pick the
// account. The user can accept it with Enter or type their own.
func proposeDescription(alias, email string) string {
	domain := emailDomain(email)
	switch alias {
	case "personal":
		return "my personal gmail; use it when I say personal or private"
	case "work":
		return fmt.Sprintf("the mailbox at %s; use it by default and for anything about work", domain)
	}
	return fmt.Sprintf("the mailbox at %s; use it when I mention %s", domain, alias)
}
```

- [ ] **Step 4: Split `runAddAccount`**

In `cmd/minuar-multi-gmail/addaccount.go`, replace the body of `runAddAccount` from `creds, err := googleauth.LoadClientCreds(d.store)` through the `config.Save` rollback with calls to two new functions, so the function reads:

```go
func runAddAccount(ctx context.Context, d addAccountDeps, args addAccountArgs) error {
	alias, replace := args.alias, args.replace
	if !config.ValidAlias(alias) {
		return fmt.Errorf("invalid alias %q: lowercase letters, digits and dashes, starting with a letter, at most 32 characters", alias)
	}
	if args.hasDesc {
		if err := config.ValidDescription(args.description); err != nil {
			return err
		}
	}
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	if _, exists := cfg.Find(alias); exists && !replace {
		return fmt.Errorf("alias %q already exists; run minuar-multi-gmail add-account %s --replace to log in again", alias, alias)
	}
	if _, exists := cfg.Find(alias); !exists && replace {
		return fmt.Errorf("alias %q does not exist; drop --replace", alias)
	}
	tok, email, err := loginAccount(ctx, d.store, d.login, d.profile)
	if err != nil {
		return err
	}
	description := ""
	if existing, ok := cfg.Find(alias); ok {
		description = existing.Description
	}
	switch {
	case args.hasDesc:
		description = args.description
	case d.prompt && d.in != nil:
		answer, err := askDescription(d.in, d.out, description)
		if err != nil {
			return err
		}
		// An empty answer skips the question and keeps what is stored.
		if answer != "" {
			if err := config.ValidDescription(answer); err != nil {
				return err
			}
			description = answer
		}
	}
	acct := config.Account{Alias: alias, Email: email, Description: description, AddedAt: d.now().UTC()}
	if err := saveAccount(d.configDir, d.store, cfg, acct, tok, replace); err != nil {
		return err
	}
	fmt.Fprintf(d.out, "Connected %s as %q.", email, alias)
	if cfg.Default == alias {
		fmt.Fprint(d.out, " It is the default account.")
	}
	fmt.Fprintln(d.out)
	if description == "" {
		fmt.Fprintf(d.out, "No description set. Add one with: minuar-multi-gmail set-description %s \"<text>\"\n", alias)
	}
	return nil
}

// loginAccount loads the OAuth client, runs the browser login and returns
// the token with the address it belongs to. The wizard calls it before it
// knows the alias, because the address is what the alias is proposed from.
func loginAccount(ctx context.Context, store secrets.Store,
	login func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error),
	profile func(context.Context, *oauth2.Token) (string, error)) (*oauth2.Token, string, error) {
	creds, err := googleauth.LoadClientCreds(store)
	if err != nil {
		return nil, "", err
	}
	tok, err := login(ctx, creds)
	if err != nil {
		return nil, "", err
	}
	email, err := profile(ctx, tok)
	if err != nil {
		return nil, "", fmt.Errorf("read account email: %w", err)
	}
	return tok, email, nil
}

// saveAccount records the account and its refresh token. The token is
// stored before the config so a failed save can remove it again; a config
// without the account and a store without the token is the clean state.
func saveAccount(configDir string, store secrets.Store, cfg *config.File, acct config.Account, tok *oauth2.Token, replace bool) error {
	var err error
	if replace {
		err = cfg.Replace(acct)
	} else {
		err = cfg.Add(acct)
	}
	if err != nil {
		return err
	}
	key := secrets.RefreshTokenKey(acct.Alias)
	if err := store.Set(key, tok.RefreshToken); err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	if err := config.Save(configDir, cfg); err != nil {
		store.Delete(key)
		return err
	}
	return nil
}
```

Keep the rest of the file as it is. The `cfg.Default == alias` print still works because `cfg.Add` set the default on the same struct.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`; every existing `TestRunAddAccount*` test passes without change.

- [ ] **Step 6: Commit**

```bash
git add cmd/minuar-multi-gmail/addaccount.go cmd/minuar-multi-gmail/wizard_propose.go cmd/minuar-multi-gmail/wizard_propose_test.go
git commit -m "refactor(add-account): split login from save; propose alias and description" -m "The wizard needs the address before it can propose an alias, so the login half and the save half of add-account become loginAccount and saveAccount. proposeAlias and proposeDescription turn an address into the defaults the wizard offers."
```

---

### Task 3: Extract `storeClient` from `runSetup`

**Files:**
- Modify: `cmd/minuar-multi-gmail/setup.go:63-117`
- Modify: `cmd/minuar-multi-gmail/commands_test.go` (add one test)

**Interfaces:**
- Consumes: `googleauth.ParseClientSecretFile`, `googleauth.SaveClientCreds`, `config.Load`, `config.Save`, `secrets.Kind.Describe`.
- Produces: `func storeClient(configDir string, kind secrets.Kind, store secrets.Store, cfg *config.File, creds googleauth.ClientCreds, out io.Writer) error` — saves the credentials, records the kind, prints "Stored OAuth client <id> in the <Describe>." (one line, no `rm` hint; `runSetup` prints the `rm` hint and the "Next:" line itself after the call).

- [ ] **Step 1: Write the failing test**

Append to `commands_test.go`:

```go
func TestStoreClientRecordsTheKindAndKeepsAccounts(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.File{Version: 1, Default: "work", Accounts: []config.Account{{Alias: "work", Email: "you@company.example", AddedAt: fixedNow()}}}
	st := secrets.NewMem()
	var out bytes.Buffer
	err := storeClient(dir, secrets.KindFile, st, cfg, googleauth.ClientCreds{ID: "id.apps", Secret: "GOCSPX-x"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := st.Get(secrets.ClientIDKey); id != "id.apps" {
		t.Fatalf("client id = %q", id)
	}
	got, _ := config.Load(dir)
	if got.Secrets != "file" || got.Default != "work" || len(got.Accounts) != 1 {
		t.Fatalf("config = %+v", got)
	}
	want := "Stored OAuth client id.apps in the file " + filepath.Join(dir, secrets.FileName) + ".\n"
	if out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/... -run StoreClient`
Expected: `undefined: storeClient`.

- [ ] **Step 3: Extract the function**

In `setup.go`, replace the tail of `runSetup` from `store, err := d.open(kind)` to the end with:

```go
	store, err := d.open(kind)
	if err != nil {
		return err
	}
	if err := storeClient(d.configDir, kind, store, cfg, creds, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "Delete the downloaded file now:\n  rm %s\nNext: minuar-multi-gmail add-account <alias>\n", path)
	return nil
}

// storeClient saves the OAuth client in store and records kind in
// accounts.json. It is the last half of setup and the wizard's way to
// store a file it found by itself.
func storeClient(configDir string, kind secrets.Kind, store secrets.Store, cfg *config.File, creds googleauth.ClientCreds, out io.Writer) error {
	if err := googleauth.SaveClientCreds(store, creds); err != nil {
		return fmt.Errorf("store in %s: %w", kind.Describe(configDir), err)
	}
	cfg.Secrets = string(kind)
	if err := config.Save(configDir, cfg); err != nil {
		return fmt.Errorf("credentials are stored in %s but not recorded in accounts.json: %w; fix the file and run setup again", kind.Describe(configDir), err)
	}
	fmt.Fprintf(out, "Stored OAuth client %s in the %s.\n", creds.ID, kind.Describe(configDir))
	return nil
}
```

The existing setup tests check for `"Stored OAuth client"` prefixes, `"rm " + path` and the "Next:" line; the combined output is unchanged, so they pass as they are.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/minuar-multi-gmail/setup.go cmd/minuar-multi-gmail/commands_test.go
git commit -m "refactor(setup): extract storeClient for the wizard" -m "The storing half of setup (save the client, record the store kind) becomes storeClient so the wizard can call it with the store it already chose and the acceptance it already asked for."
```

---

### Task 4: Steps w1 (secret store) and w2 (Claude Code)

**Files:**
- Create: `cmd/minuar-multi-gmail/wizard_store.go`
- Create: `cmd/minuar-multi-gmail/wizard_claude.go`
- Create: `cmd/minuar-multi-gmail/wizard_steps_test.go`

**Interfaces:**
- Consumes: `wizardDeps`, `ask`, `askYesNo`, `heading`, `secrets.Detect` shape, `secrets.Kind`, `secrets.KindFile`, `secrets.Kind.Describe`, `config.Load`.
- Produces:
  - `func wizardStore(d *wizardDeps) (secrets.Kind, bool, error)` — returns the kind to use and whether the file store was accepted in this run (true when the user answered yes, or when the environment forced a kind, or when the kind is not file). Prints; writes nothing.
  - `var errStoreRefused = errors.New("no secret store accepted; install a keyring (see the sentence above) and run the wizard again")`.
  - `func wizardClaude(d *wizardDeps)` — never fails; prints what it did.
  - `func manualRegisterCommand(binary string) string` — returns `claude mcp add --scope user gmail -- <binary> serve`.

- [ ] **Step 1: Write the failing tests**

`cmd/minuar-multi-gmail/wizard_steps_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// stepDeps is a wizardDeps for one step: scripted stdin, captured stdout,
// a temp config dir, and fakes that record what they were asked.
type stepDeps struct {
	d    *wizardDeps
	out  *bytes.Buffer
	runs []string
}

func newStepDeps(t *testing.T, input, goos string, kind secrets.Kind, why string) *stepDeps {
	t.Helper()
	var out bytes.Buffer
	s := &stepDeps{out: &out}
	s.d = &wizardDeps{
		in: strings.NewReader(input), out: &out,
		configDir: t.TempDir(), downloads: t.TempDir(), binary: "/opt/mmg", goos: goos,
		detect:   func(string) (secrets.Kind, string) { return kind, why },
		open:     func(k secrets.Kind) (secrets.Store, error) { return secrets.NewMem(), nil },
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run: func(name string, args ...string) (string, error) {
			s.runs = append(s.runs, name+" "+strings.Join(args, " "))
			return "", nil
		},
		openURL: func(string) error { return nil },
	}
	return s
}

func TestWizardStoreRecordedKindSkips(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "should not be called")
	if err := config.Save(s.d.configDir, &config.File{Version: 1, Secrets: "secret-service"}); err != nil {
		t.Fatal(err)
	}
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindSecretService || !accepted {
		t.Fatalf("kind %q accepted %v err %v", kind, accepted, err)
	}
	if !strings.Contains(s.out.String(), "Secrets go to Secret Service keyring (secret-tool)") || strings.Contains(s.out.String(), "should not be called") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardStoreDarwin(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "Secrets go to the macOS Keychain.")
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindKeychain || !accepted {
		t.Fatalf("kind %q accepted %v err %v", kind, accepted, err)
	}
	if !strings.Contains(s.out.String(), "Secrets go to the macOS Keychain.") || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardStoreLinuxWithoutKeyringAsks(t *testing.T) {
	why := "No keyring found: secret-tool is not installed."
	s := newStepDeps(t, "n\n", "linux", secrets.KindFile, why)
	_, _, err := wizardStore(s.d)
	if !errors.Is(err, errStoreRefused) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(s.out.String(), why) || !strings.Contains(s.out.String(), "Store tokens in a private file, readable by your user account and root? [y/N]") {
		t.Fatalf("out = %q", s.out.String())
	}
	if entries, _ := os.ReadDir(s.d.configDir); len(entries) != 0 {
		t.Fatalf("nothing may be written on refusal: %v", entries)
	}

	s = newStepDeps(t, "y\n", "linux", secrets.KindFile, why)
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !accepted {
		t.Fatalf("kind %q accepted %v err %v", kind, accepted, err)
	}
	if entries, _ := os.ReadDir(s.d.configDir); len(entries) != 0 {
		t.Fatalf("w1 must not write; setup records the kind later: %v", entries)
	}
}

func TestWizardStoreWindowsNeedsNoAcceptance(t *testing.T) {
	s := newStepDeps(t, "", "windows", secrets.KindFile, "This version has no keyring store for Windows.")
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !accepted || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("kind %q accepted %v err %v out %q", kind, accepted, err, s.out.String())
	}
}

func TestWizardStoreEnvForcedNeedsNoAcceptance(t *testing.T) {
	s := newStepDeps(t, "", "linux", secrets.KindFile, "Secret store forced to file by MINUAR_MULTI_GMAIL_SECRETS.")
	s.d.envKind = "file"
	kind, accepted, err := wizardStore(s.d)
	if err != nil || kind != secrets.KindFile || !accepted || strings.Contains(s.out.String(), "[y/N]") {
		t.Fatalf("kind %q accepted %v err %v out %q", kind, accepted, err, s.out.String())
	}
}

func TestWizardStoreUnknownEnvKind(t *testing.T) {
	s := newStepDeps(t, "", "linux", "", `unknown secret store "vault"`)
	if _, _, err := wizardStore(s.d); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("err = %v", err)
	}
}

func TestWizardClaudeNotOnPath(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	wizardClaude(s.d)
	if len(s.runs) != 0 || !strings.Contains(s.out.String(), "Claude Code is not on PATH") || !strings.Contains(s.out.String(), "claude mcp add --scope user gmail -- /opt/mmg serve") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeAlreadyRegistered(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	wizardClaude(s.d)
	if strings.Join(s.runs, ";") != "claude mcp get gmail" || !strings.Contains(s.out.String(), "Already registered in Claude Code") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeRegisters(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	s.d.run = func(name string, args ...string) (string, error) {
		s.runs = append(s.runs, name+" "+strings.Join(args, " "))
		if args[1] == "get" {
			return "No MCP server found with name: gmail", errors.New("exit status 1")
		}
		return "Added stdio MCP server gmail", nil
	}
	wizardClaude(s.d)
	want := "claude mcp get gmail;claude mcp add --scope user gmail -- /opt/mmg serve"
	if strings.Join(s.runs, ";") != want || !strings.Contains(s.out.String(), "Registered in Claude Code") {
		t.Fatalf("runs %v out %q", s.runs, s.out.String())
	}
}

func TestWizardClaudeAddFailureIsNotFatal(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	s.d.lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	s.d.run = func(name string, args ...string) (string, error) {
		return "boom", errors.New("exit status 1")
	}
	wizardClaude(s.d)
	if !strings.Contains(s.out.String(), "boom") || !strings.Contains(s.out.String(), "claude mcp add --scope user gmail -- /opt/mmg serve") {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestManualRegisterCommand(t *testing.T) {
	if got := manualRegisterCommand("/opt/mmg"); got != "claude mcp add --scope user gmail -- /opt/mmg serve" {
		t.Fatalf("got %q", got)
	}
	_ = filepath.Join // keep the import used for later tasks' tests in this file
}
```

(Remove the `_ = filepath.Join` line and the `path/filepath` import if the compiler reports the import as unused after Task 5 adds its tests; Task 5 uses it.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run 'WizardStore|WizardClaude|ManualRegister'`
Expected: `undefined: wizardStore`, `undefined: wizardClaude`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/wizard_store.go`**

```go
package main

import (
	"errors"
	"fmt"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

var errStoreRefused = errors.New("no secret store accepted; install a keyring (see the sentence above) and run the wizard again")

// wizardStore is step w1. It returns the kind every later step uses and
// whether the file store counts as accepted. It writes nothing: setup
// records the kind when the client is stored.
func wizardStore(d *wizardDeps) (secrets.Kind, bool, error) {
	heading(d, "Secret store")
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return "", false, err
	}
	if cfg.Secrets != "" {
		kind := secrets.Kind(cfg.Secrets)
		fmt.Fprintf(d.out, "Secrets go to %s (recorded on an earlier run).\n", kind.Describe(d.configDir))
		return kind, true, nil
	}
	kind, why := d.detect(secrets.ServiceName())
	if kind == "" {
		return "", false, errors.New(why)
	}
	fmt.Fprintln(d.out, why)
	if kind != secrets.KindFile || d.goos == "windows" || d.envKind != "" {
		return kind, true, nil
	}
	yes, err := askYesNo(d, "Store tokens in a private file, readable by your user account and root?", false)
	if err != nil {
		return "", false, err
	}
	if !yes {
		return "", false, errStoreRefused
	}
	return kind, true, nil
}
```

- [ ] **Step 4: Write `cmd/minuar-multi-gmail/wizard_claude.go`**

```go
package main

import (
	"fmt"
	"strings"
)

func manualRegisterCommand(binary string) string {
	return "claude mcp add --scope user gmail -- " + binary + " serve"
}

// wizardClaude is step w2: register the server in Claude Code at user
// scope. Nothing here is fatal; the manual command is always printed when
// the wizard could not do it.
func wizardClaude(d *wizardDeps) {
	heading(d, "Claude Code")
	if _, err := d.lookPath("claude"); err != nil {
		fmt.Fprintf(d.out, "Claude Code is not on PATH. Register later with:\n  %s\n", manualRegisterCommand(d.binary))
		return
	}
	if _, err := d.run("claude", "mcp", "get", "gmail"); err == nil {
		fmt.Fprintln(d.out, "Already registered in Claude Code.")
		return
	}
	out, err := d.run("claude", "mcp", "add", "--scope", "user", "gmail", "--", d.binary, "serve")
	if err != nil {
		fmt.Fprintf(d.out, "Registration failed: %s\nRegister later with:\n  %s\n", strings.TrimSpace(out), manualRegisterCommand(d.binary))
		return
	}
	fmt.Fprintln(d.out, "Registered in Claude Code (user scope, every project).")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`.

- [ ] **Step 6: Commit**

```bash
git add cmd/minuar-multi-gmail/wizard_store.go cmd/minuar-multi-gmail/wizard_claude.go cmd/minuar-multi-gmail/wizard_steps_test.go
git commit -m "feat(wizard): choose the secret store and register with Claude Code" -m "w1 reuses Detect and asks for the file store with a yes/no instead of an environment variable; a recorded kind skips the probe. w2 runs claude mcp get/add when claude is on PATH and prints the manual command otherwise. Neither step is fatal beyond a refused file store."
```

---

### Task 5: Step w3 (OAuth client through the console, Downloads watcher)

**Files:**
- Create: `cmd/minuar-multi-gmail/wizard_client.go`
- Modify: `cmd/minuar-multi-gmail/wizard_steps_test.go` (append tests)

**Interfaces:**
- Consumes: `wizardDeps`, `ask`, `askYesNo`, `heading`, `storeClient` (Task 3), `googleauth.LoadClientCreds`, `googleauth.ParseClientSecretFile`, `config.Load`.
- Produces:
  - `type consolePage struct{ URL, Instruction string }` and `var consolePages = [4]consolePage{...}` exactly per spec §6 w3.
  - `func watchDownloads(d *wizardDeps, since time.Time) (string, error)` — polls every second for `client_secret*.json` with mtime after `since`, up to `d.waitFile`; selects on `d.lines()` between polls: a non-empty line is a pasted path and wins.
  - `func wizardClient(ctx context.Context, d *wizardDeps, kind secrets.Kind, store secrets.Store) error`.
  - `var errNoClientFile` with the message "no client file appeared in <downloads> within <waitFile>; download it and run the wizard again" built by `fmt.Errorf`.

- [ ] **Step 1: Write the failing tests**

Append to `wizard_steps_test.go`:

```go
func writeClientFile(t *testing.T, dir, name string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(sampleClientSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

// A clock the watcher advances itself: sleep moves it forward and fires
// the scheduled file write.
func fakeClock(start time.Time) (now func() time.Time, sleep func(time.Duration), after func(time.Duration, func())) {
	t := start
	var jobs []struct {
		at time.Time
		fn func()
	}
	now = func() time.Time { return t }
	sleep = func(d time.Duration) {
		t = t.Add(d)
		for i := 0; i < len(jobs); i++ {
			if !jobs[i].at.After(t) {
				jobs[i].fn()
				jobs = append(jobs[:i], jobs[i+1:]...)
				i--
			}
		}
	}
	after = func(d time.Duration, fn func()) {
		jobs = append(jobs, struct {
			at time.Time
			fn func()
		}{t.Add(d), fn})
	}
	return
}

func TestWatchDownloadsFindsANewFile(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, 10*time.Minute
	// An old download from an earlier attempt must be ignored.
	writeClientFile(t, s.d.downloads, "client_secret_old.json", start.Add(-time.Hour))
	after(3*time.Second, func() { writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	got, err := watchDownloads(s.d, start)
	if err != nil || filepath.Base(got) != "client_secret_new.json" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWatchDownloadsTimesOut(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, _ := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, 5*time.Second
	_, err := watchDownloads(s.d, start)
	if err == nil || !strings.Contains(err.Error(), "no client file appeared in "+s.d.downloads+" within 5s") {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchDownloadsAcceptsAPastedPath(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, _ := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	elsewhere := writeClientFile(t, t.TempDir(), "client_secret_x.json", start.Add(-time.Hour))
	s.d.in = strings.NewReader(elsewhere + "\n")
	got, err := watchDownloads(s.d, start)
	if err != nil || got != elsewhere {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWatchDownloadsEmptyLineKeepsWaiting(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	s.d.in = strings.NewReader("\n")
	after(2*time.Second, func() { writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	got, err := watchDownloads(s.d, start)
	if err != nil || filepath.Base(got) != "client_secret_new.json" {
		t.Fatalf("got %q, %v", got, err)
	}
	if !strings.Contains(s.out.String(), "still waiting for the file in "+s.d.downloads) {
		t.Fatalf("out = %q", s.out.String())
	}
}

func TestWizardClientSkipsWhenStored(t *testing.T) {
	s := newStepDeps(t, "", "darwin", secrets.KindKeychain, "")
	st := seededStore(t)
	opened := 0
	s.d.openURL = func(string) error { opened++; return nil }
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, st); err != nil {
		t.Fatal(err)
	}
	if opened != 0 || !strings.Contains(s.out.String(), "OAuth client stored") {
		t.Fatalf("opened %d out %q", opened, s.out.String())
	}
}

func TestWizardClientGuidesTheConsoleAndStoresTheDownload(t *testing.T) {
	// Enter for pages 1-3, then the file appears while page 4 is open, then
	// "y" to delete the download.
	s := newStepDeps(t, "\n\n\n"+"y\n", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	var opened []string
	s.d.openURL = func(u string) error { opened = append(opened, u); return nil }
	var path string
	after(2*time.Second, func() { path = writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	st := secrets.NewMem()
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, st); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 4 || opened[0] != consolePages[0].URL || opened[3] != consolePages[3].URL {
		t.Fatalf("opened = %v", opened)
	}
	for _, p := range consolePages {
		if !strings.Contains(s.out.String(), p.Instruction) || !strings.Contains(s.out.String(), p.URL) {
			t.Fatalf("out lacks page %q", p.URL)
		}
	}
	if id, _ := st.Get(secrets.ClientIDKey); id != "123-abc.apps.googleusercontent.com" {
		t.Fatalf("client id = %q", id)
	}
	cfg, _ := config.Load(s.d.configDir)
	if cfg.Secrets != "keychain" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the download must be deleted after y: %v", err)
	}
}

func TestWizardClientKeepsTheDownloadOnNo(t *testing.T) {
	s := newStepDeps(t, "\n\n\n"+"n\n", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	var path string
	after(2*time.Second, func() { path = writeClientFile(t, s.d.downloads, "client_secret_new.json", now().Add(time.Second)) })
	if err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the download must stay after n: %v", err)
	}
}

func TestWizardClientEOFWhileWaitingForEnter(t *testing.T) {
	s := newStepDeps(t, "\n", "darwin", secrets.KindKeychain, "")
	err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem())
	if !errors.Is(err, errInputEnded) {
		t.Fatalf("err = %v, want errInputEnded", err)
	}
}

func TestWizardClientRejectsABadFile(t *testing.T) {
	s := newStepDeps(t, "\n\n\n", "darwin", secrets.KindKeychain, "")
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now, sleep, after := fakeClock(start)
	s.d.now, s.d.sleep, s.d.waitFile = now, sleep, time.Minute
	after(time.Second, func() {
		p := filepath.Join(s.d.downloads, "client_secret_bad.json")
		os.WriteFile(p, []byte("{not json"), 0o600)
		os.Chtimes(p, now().Add(time.Second), now().Add(time.Second))
	})
	err := wizardClient(context.Background(), s.d, secrets.KindKeychain, secrets.NewMem())
	if err == nil || !strings.Contains(err.Error(), "client_secret_bad.json") {
		t.Fatalf("err = %v", err)
	}
}
```

Add `"context"` and `"time"` to the file's imports (and keep `path/filepath`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run 'WatchDownloads|WizardClient'`
Expected: `undefined: watchDownloads`, `undefined: wizardClient`, `undefined: consolePages`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/wizard_client.go`**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

type consolePage struct{ URL, Instruction string }

// consolePages are the four Google Cloud console pages a user visits to
// get an OAuth client, in order. Google has no API for any of them.
var consolePages = [4]consolePage{
	{"https://console.cloud.google.com/projectcreate", "Create a project. Name: minuar-multi-gmail. Press Enter here when it exists."},
	{"https://console.cloud.google.com/apis/library/gmail.googleapis.com", "Click Enable. Press Enter when done."},
	{"https://console.cloud.google.com/auth/overview", "Configure the consent screen: app name minuar-multi-gmail, your email, audience External, then publish it (Audience page, \"Publish app\"). Press Enter when done."},
	{"https://console.cloud.google.com/auth/clients/create", "Application type Desktop app, name minuar-multi-gmail, Create, then Download JSON."},
}

const clientFilePattern = "client_secret*.json"

// newestClientFile returns the newest client_secret*.json in dir with a
// modification time after since, or "".
func newestClientFile(dir string, since time.Time) string {
	matches, _ := filepath.Glob(filepath.Join(dir, clientFilePattern))
	var best string
	var bestTime time.Time
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || !st.Mode().IsRegular() || !st.ModTime().After(since) {
			continue
		}
		if best == "" || st.ModTime().After(bestTime) {
			best, bestTime = m, st.ModTime()
		}
	}
	return best
}

// watchDownloads waits for the client file: a new client_secret*.json in
// the Downloads directory, or a path pasted on stdin. An empty line on
// stdin re-prints the hint and keeps waiting. A closed stdin ends the
// pasted-path route only; the watcher goes on until the file appears or
// the wait runs out.
func watchDownloads(d *wizardDeps, since time.Time) (string, error) {
	deadline := d.now().Add(d.waitFile)
	input := d.lines()
	for {
		if p := newestClientFile(d.downloads, since); p != "" {
			return p, nil
		}
		select {
		case r, ok := <-input:
			switch {
			case !ok:
				input = nil // a nil channel never fires again
			case r.err != nil:
				return "", r.err
			case strings.TrimSpace(r.line) != "":
				return strings.TrimSpace(r.line), nil
			default:
				fmt.Fprintf(d.out, "still waiting for the file in %s; paste its path here if it is elsewhere\n", d.downloads)
			}
		default:
		}
		if !d.now().Before(deadline) {
			return "", fmt.Errorf("no client file appeared in %s within %s; download it and run the wizard again", d.downloads, d.waitFile)
		}
		d.sleep(time.Second)
	}
}

// wizardClient is step w3: the guided console, the Downloads watcher, and
// the store through storeClient.
func wizardClient(ctx context.Context, d *wizardDeps, kind secrets.Kind, store secrets.Store) error {
	heading(d, "OAuth client")
	if _, err := googleauth.LoadClientCreds(store); err == nil {
		fmt.Fprintln(d.out, "OAuth client stored.")
		return nil
	}
	fmt.Fprintln(d.out, "Google needs an OAuth client for this tool. The browser opens four console pages, one at a time.")
	started := d.now()
	for i, page := range consolePages {
		fmt.Fprintf(d.out, "\n%d/4  %s\n     %s\n", i+1, page.Instruction, page.URL)
		if err := d.openURL(page.URL); err != nil {
			fmt.Fprintf(d.out, "     (could not open a browser: %v; open the address yourself)\n", err)
		}
		if i < 3 {
			if _, err := d.readLine(); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(d.out, "Waiting for the downloaded file in %s (paste its path here if it lands elsewhere).\n", d.downloads)
	path, err := watchDownloads(d, started)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	creds, err := googleauth.ParseClientSecretFile(data)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	if err := storeClient(d.configDir, kind, store, cfg, creds, d.out); err != nil {
		return err
	}
	del, err := askYesNo(d, "Delete the downloaded file?", true)
	if err != nil {
		return err
	}
	if del {
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(d.out, "could not delete %s: %v\n", path, err)
		} else {
			fmt.Fprintf(d.out, "Deleted %s.\n", path)
		}
	}
	return nil
}
```

Why this works without stealing input: `lines()` is one goroutine over one reader that reads at most one line ahead. While the watcher polls, a line the user types sits in the unbuffered channel until the watcher's `select` takes it; when the watcher returns because the file appeared, an untouched line stays there for the next `askYesNo`. The guided test pins the second case (file first, then "y" reaches the delete question) and the pasted-path test pins the first.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./cmd/... -run 'WatchDownloads|WizardClient' -count=3` then `./scripts/verify.sh`
Expected: pass three times under the race detector; `verify: ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/minuar-multi-gmail/wizard_client.go cmd/minuar-multi-gmail/wizard_steps_test.go
git commit -m "feat(wizard): guide the console pages and pick up the downloaded client" -m "w3 opens the four console pages one at a time, watches the Downloads directory for a client_secret*.json newer than the wizard start, accepts a pasted path, stores the client through storeClient and offers to delete the download. A closed stdin ends the pasted-path route without stopping the watcher."
```

---

### Task 6: Steps w4, w5, w6 and `runWizard`

**Files:**
- Create: `cmd/minuar-multi-gmail/wizard_accounts.go`
- Create: `cmd/minuar-multi-gmail/wizard_finish.go`
- Modify: `cmd/minuar-multi-gmail/wizard.go` (`runWizard`)
- Create: `cmd/minuar-multi-gmail/wizard_flow_test.go`

**Interfaces:**
- Consumes: `loginAccount`, `saveAccount`, `proposeAlias`, `proposeDescription` (Task 2), `wizardStore`, `wizardClaude` (Task 4), `wizardClient` (Task 5), `runSetDefault`, `runDoctor`, `doctorDeps`, `config.Load`, `config.ValidAlias`, `config.ValidDescription`.
- Produces:
  - `func wizardAccounts(ctx context.Context, d *wizardDeps, store secrets.Store) error`.
  - `func wizardDefault(d *wizardDeps) error`.
  - `func wizardCheck(ctx context.Context, d *wizardDeps, store secrets.Store, kind secrets.Kind)`.
  - `runWizard` filled in.

- [ ] **Step 1: Write the failing tests**

`cmd/minuar-multi-gmail/wizard_flow_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
)

// flowDeps builds a wizardDeps whose login returns the next address from
// emails on each call, with everything else faked as done.
func flowDeps(t *testing.T, input string, st secrets.Store, emails ...string) (*wizardDeps, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	i := 0
	d := &wizardDeps{
		in: strings.NewReader(input), out: &out,
		configDir: t.TempDir(), downloads: t.TempDir(), binary: "/opt/mmg", goos: "darwin",
		detect:   func(string) (secrets.Kind, string) { return secrets.KindKeychain, "Secrets go to the macOS Keychain." },
		open:     func(secrets.Kind) (secrets.Store, error) { return st, nil },
		lookPath: func(string) (string, error) { return "/usr/local/bin/claude", nil },
		run:      func(string, ...string) (string, error) { return "", nil },
		openURL:  func(string) error { return nil },
		login: func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: "at", RefreshToken: "rt-" + emails[i]}, nil
		},
		profile: func(context.Context, *oauth2.Token) (string, error) {
			e := emails[i]
			i++
			return e, nil
		},
		doctorProfile: func(ctx context.Context, ts oauth2.TokenSource) (string, error) {
			tok, err := ts.Token()
			if err != nil {
				return "", err
			}
			return strings.TrimPrefix(tok.RefreshToken, "rt-"), nil
		},
		now: func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		sleep: func(time.Duration) {}, waitFile: time.Minute,
	}
	return d, &out
}

func TestWizardAccountsProposesAndAccepts(t *testing.T) {
	st := seededStore(t)
	// Round 1: Y, Enter (alias personal), Enter (description). Round 2: y,
	// Enter (work), typed description. Round 3: n.
	d, out := flowDeps(t, "\n\n\ny\n\nthe shop mailbox; use it for orders\nn\n", st, "ann@gmail.com", "ann@minuar.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 2 || cfg.Accounts[0].Alias != "personal" || cfg.Accounts[1].Alias != "work" || cfg.Default != "personal" {
		t.Fatalf("accounts = %+v default %q", cfg.Accounts, cfg.Default)
	}
	if cfg.Accounts[0].Description != "my personal gmail; use it when I say personal or private" || cfg.Accounts[1].Description != "the shop mailbox; use it for orders" {
		t.Fatalf("descriptions = %q, %q", cfg.Accounts[0].Description, cfg.Accounts[1].Description)
	}
	if rt, _ := st.Get(secrets.RefreshTokenKey("work")); rt != "rt-ann@minuar.com" {
		t.Fatalf("refresh token = %q", rt)
	}
	s := out.String()
	for _, want := range []string{"Add your first Gmail account? [Y/n]", "Alias [personal]", "When should Claude use it? [my personal gmail; use it when I say personal or private]", "Connected ann@gmail.com as \"personal\".", "Add a Gmail account? [Y/n]", "Alias [work]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("out lacks %q: %q", want, s)
		}
	}
}

func TestWizardAccountsTakenAliasGetsASuffix(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "\n\n\n"+"y\n\n\n"+"n\n", st, "ann@gmail.com", "bob@gmail.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 2 || cfg.Accounts[1].Alias != "personal2" || !strings.Contains(out.String(), "Alias [personal2]") {
		t.Fatalf("accounts = %+v out %q", cfg.Accounts, out.String())
	}
}

func TestWizardAccountsInvalidAliasReAsks(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "\nBad Alias!\nhouse\n\nn\n", st, "ann@gmail.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Alias != "house" || strings.Count(out.String(), "Alias [personal]") != 2 || !strings.Contains(out.String(), "lowercase letters") {
		t.Fatalf("accounts = %+v out %q", cfg.Accounts, out.String())
	}
}

func TestWizardAccountsDuplicateEmailIsReported(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "\n\n\n"+"y\n\n\n"+"n\n", st, "ann@gmail.com", "ann@gmail.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 1 || !strings.Contains(out.String(), "already connected") {
		t.Fatalf("accounts = %+v out %q", cfg.Accounts, out.String())
	}
}

func TestWizardAccountsNoneAdded(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "n\n", st)
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no account added; run the wizard again to add one") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestWizardAccountsLoginFailureStopsTheWizard(t *testing.T) {
	st := seededStore(t)
	d, _ := flowDeps(t, "\n", st, "ann@gmail.com")
	d.login = func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) { return nil, errors.New("login: timed out") }
	err := wizardAccounts(context.Background(), d, st)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestWizardDefault(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "", st)
	cfg := &config.File{Version: 1}
	cfg.Add(config.Account{Alias: "personal", Email: "a@gmail.com", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	if err := wizardDefault(d); err != nil || strings.Contains(out.String(), "Default account") {
		t.Fatalf("one account must not ask: %v %q", err, out.String())
	}

	cfg.Add(config.Account{Alias: "work", Email: "a@minuar.com", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	d.in = strings.NewReader("nope\nwork\n")
	d.lineCh = nil
	if err := wizardDefault(d); err != nil {
		t.Fatal(err)
	}
	got, _ := config.Load(d.configDir)
	if got.Default != "work" || strings.Count(out.String(), "Default account [personal]") != 2 || !strings.Contains(out.String(), "no account \"nope\"") {
		t.Fatalf("default %q out %q", got.Default, out.String())
	}

	d.in = strings.NewReader("\n")
	d.lineCh = nil
	out.Reset()
	if err := wizardDefault(d); err != nil || !strings.Contains(out.String(), "Default account [work]") {
		t.Fatalf("Enter keeps the default: %v %q", err, out.String())
	}
}

func TestRunWizardEverythingDoneSkips(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "n\n", st)
	cfg := &config.File{Version: 1, Secrets: "keychain"}
	cfg.Add(config.Account{Alias: "work", Email: "a@minuar.com", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	st.Set(secrets.RefreshTokenKey("work"), "rt-a@minuar.com")
	if err := runWizard(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"== Secret store", "recorded on an earlier run", "== Claude Code", "Already registered in Claude Code", "== OAuth client", "OAuth client stored", "== Accounts", "Add a Gmail account? [Y/n]", "== Check", "ok   secrets", "ok   work", "Restart Claude Code, then ask it about your mail."} {
		if !strings.Contains(s, want) {
			t.Fatalf("out lacks %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "[") != 1 {
		t.Fatalf("the only prompt must be the account question:\n%s", s)
	}
}

func TestRunWizardFreshRunOnDarwin(t *testing.T) {
	st := secrets.NewMem()
	// w3: Enter x3, then a pasted path, then y (delete). w4: Y, Enter,
	// Enter, then n. w5: one account, no question.
	clientPath := writeClientFile(t, t.TempDir(), "client_secret_x.json", time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC))
	d, out := flowDeps(t, "\n\n\n"+clientPath+"\ny\n"+"\n\n\n"+"n\n", st, "ann@minuar.com")
	if err := runWizard(context.Background(), d); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, _ := config.Load(d.configDir)
	if cfg.Secrets != "keychain" || len(cfg.Accounts) != 1 || cfg.Accounts[0].Alias != "work" || cfg.Default != "work" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := os.Stat(clientPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the pasted file must be deleted after y")
	}
	if !strings.Contains(out.String(), "ok   work           ann@minuar.com") {
		t.Fatalf("doctor line missing:\n%s", out.String())
	}
}

func TestRunWizardLinuxRefusalWritesNothing(t *testing.T) {
	st := secrets.NewMem()
	d, _ := flowDeps(t, "n\n", st)
	d.goos = "linux"
	d.detect = func(string) (secrets.Kind, string) { return secrets.KindFile, "No keyring found: secret-tool is not installed." }
	runs := 0
	d.run = func(string, ...string) (string, error) { runs++; return "", nil }
	err := runWizard(context.Background(), d)
	if !errors.Is(err, errStoreRefused) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(d.configDir); len(entries) != 0 || runs != 0 {
		t.Fatalf("nothing may happen after a refusal: entries %v runs %d", entries, runs)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run 'WizardAccounts|WizardDefault|RunWizard'`
Expected: `undefined: wizardAccounts`, `undefined: wizardDefault`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/wizard_accounts.go`**

```go
package main

import (
	"context"
	"fmt"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// wizardAccounts is step w4: log accounts in one by one, each with a
// proposed alias and description the user accepts with Enter.
func wizardAccounts(ctx context.Context, d *wizardDeps, store secrets.Store) error {
	heading(d, "Accounts")
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	added := 0
	for {
		question := "Add a Gmail account?"
		if len(cfg.Accounts) == 0 {
			question = "Add your first Gmail account?"
		}
		more, err := askYesNo(d, question, true)
		if err != nil {
			return err
		}
		if !more {
			break
		}
		fmt.Fprintln(d.out, "The browser opens for the Google login.")
		tok, email, err := loginAccount(ctx, store, d.login, d.profile)
		if err != nil {
			return err
		}
		taken := func(alias string) bool { _, ok := cfg.Find(alias); return ok }
		alias, err := askAlias(d, proposeAlias(email, taken), taken)
		if err != nil {
			return err
		}
		description, err := askWizardDescription(d, proposeDescription(alias, email))
		if err != nil {
			return err
		}
		acct := config.Account{Alias: alias, Email: email, Description: description, AddedAt: d.now().UTC()}
		if err := saveAccount(d.configDir, store, cfg, acct, tok, false); err != nil {
			// A duplicate address is the one save error the user can cause;
			// report it and offer the next round instead of stopping.
			fmt.Fprintf(d.out, "Not added: %v\n", err)
			cfg, _ = config.Load(d.configDir)
			continue
		}
		added++
		fmt.Fprintf(d.out, "Connected %s as %q.\n", email, alias)
	}
	if added == 0 && len(cfg.Accounts) == 0 {
		fmt.Fprintln(d.out, "no account added; run the wizard again to add one")
	}
	return nil
}

// askAlias asks until the answer is a valid, free alias.
func askAlias(d *wizardDeps, proposal string, taken func(string) bool) (string, error) {
	for {
		alias, err := ask(d, "Alias", proposal)
		if err != nil {
			return "", err
		}
		switch {
		case !config.ValidAlias(alias):
			fmt.Fprintln(d.out, "lowercase letters, digits and dashes, starting with a letter, at most 32 characters")
		case taken(alias):
			fmt.Fprintf(d.out, "alias %q is taken\n", alias)
		default:
			return alias, nil
		}
	}
}

// askWizardDescription asks until the answer fits the length limit.
func askWizardDescription(d *wizardDeps, proposal string) (string, error) {
	for {
		text, err := ask(d, "When should Claude use it?", proposal)
		if err != nil {
			return "", err
		}
		if err := config.ValidDescription(text); err != nil {
			fmt.Fprintln(d.out, err)
			continue
		}
		return text, nil
	}
}
```

Duplicate addresses are caught by `saveAccount` (`cfg.Add` returns "email ... is already connected as alias ..."), not before the login. The "Not added" line must contain that error text, which `TestWizardAccountsDuplicateEmailIsReported` checks through "already connected". Note that `cfg.Add` mutated nothing on that error path, and `saveAccount` returned before storing the token, so reloading `cfg` is only a safety measure.

- [ ] **Step 4: Write `cmd/minuar-multi-gmail/wizard_finish.go`**

```go
package main

import (
	"context"
	"fmt"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// wizardDefault is step w5: with several accounts, confirm or change the
// default. One account is the default already.
func wizardDefault(d *wizardDeps) error {
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	if len(cfg.Accounts) < 2 {
		return nil
	}
	heading(d, "Default account")
	for {
		alias, err := ask(d, "Default account", cfg.Default)
		if err != nil {
			return err
		}
		if _, ok := cfg.Find(alias); !ok {
			fmt.Fprintf(d.out, "no account %q; one of: %v\n", alias, cfg.Aliases())
			continue
		}
		if alias == cfg.Default {
			return nil
		}
		return runSetDefault(d.configDir, alias, d.out)
	}
}

// wizardCheck is step w6: doctor, then the last instruction.
func wizardCheck(ctx context.Context, d *wizardDeps, store secrets.Store, kind secrets.Kind) {
	heading(d, "Check")
	runDoctor(ctx, doctorDeps{store: store, storeKind: kind, configDir: d.configDir, profile: d.doctorProfile, out: d.out})
	fmt.Fprintln(d.out, "\nRestart Claude Code, then ask it about your mail.")
}
```

`cfg.Aliases()` exists in `internal/config/config.go:146`.

- [ ] **Step 5: Fill in `runWizard` in `wizard.go`**

```go
// runWizard runs the six steps in order and stops at the first error.
func runWizard(ctx context.Context, d *wizardDeps) error {
	heading(d, "Setup wizard")
	fmt.Fprintln(d.out, "Enter accepts the value in brackets. Every step skips what is already done.")
	kind, accepted, err := wizardStore(d)
	if err != nil {
		return err
	}
	if !accepted {
		return errStoreRefused
	}
	store, err := d.open(kind)
	if err != nil {
		return err
	}
	wizardClaude(d)
	if err := wizardClient(ctx, d, kind, store); err != nil {
		return err
	}
	if err := wizardAccounts(ctx, d, store); err != nil {
		return err
	}
	if err := wizardDefault(d); err != nil {
		return err
	}
	wizardCheck(ctx, d, store, kind)
	return nil
}
```

Order note: w2 (Claude Code) runs after w1 and before w3 so a refused store stops before any registration, which `TestRunWizardLinuxRefusalWritesNothing` pins.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./cmd/... -count=2` then `./scripts/verify.sh`
Expected: pass twice; `verify: ok`.

- [ ] **Step 7: Commit**

```bash
git add cmd/minuar-multi-gmail/wizard_accounts.go cmd/minuar-multi-gmail/wizard_finish.go cmd/minuar-multi-gmail/wizard.go cmd/minuar-multi-gmail/wizard_flow_test.go
git commit -m "feat(wizard): add accounts with proposals, set the default, run doctor" -m "w4 logs in first and proposes alias and description from the address; Enter accepts. w5 asks for the default only with several accounts. w6 runs doctor and tells the user to restart Claude Code. runWizard chains the six steps and stops at the first error."
```

---

### Task 7: `scripts/install.sh` and README

**Files:**
- Modify: `scripts/install.sh`
- Modify: `README.md` (Quick start section and the "Everyday commands" table)

**Interfaces:** none.

- [ ] **Step 1: Change `scripts/install.sh`**

Replace everything after the `echo "installed: ..."` line with:

```bash
if [ -t 0 ]; then
  exec "$HOME/.local/bin/minuar-multi-gmail" wizard
fi
echo
echo "Run the wizard from a terminal to finish: $HOME/.local/bin/minuar-multi-gmail wizard"
echo "Or register by hand: claude mcp add --scope user gmail -- $HOME/.local/bin/minuar-multi-gmail serve"
```

- [ ] **Step 2: Rewrite the README quick start**

Replace the "## Quick start" section (from its heading up to the line before "## Everyday commands") with:

```markdown
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
whether tokens may go to a private file.

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
```

In the "Everyday commands" table add a first row:

```markdown
| `minuar-multi-gmail wizard` | Set everything up, or add an account later. Skips what is done. |
```

- [ ] **Step 3: Verify**

Run: `bash -n scripts/install.sh && ./scripts/verify.sh`
Expected: `verify: ok`.

- [ ] **Step 4: Commit**

```bash
git add scripts/install.sh README.md
git commit -m "docs: quick start through the wizard; install.sh starts it" -m "install.sh runs the wizard when it has a terminal. The README quick start becomes clone, install, follow the wizard, with the old command sequence kept under By hand."
```

---

### Task 8: Verify by hand on this Mac (controller task)

**Files:** none. No commit.

The controller runs this, not a subagent, because it needs the real Google console and Chrome.

- [ ] **Step 1: Throwaway environment**

```bash
export MINUAR_MULTI_GMAIL_CONFIG_DIR=/tmp/mmg-wizard-test
export MINUAR_MULTI_GMAIL_KEYCHAIN_SERVICE=com.minuar.multi-gmail.wizardtest
rm -rf /tmp/mmg-wizard-test
go build -o /tmp/mmg ./cmd/minuar-multi-gmail
```

- [ ] **Step 2: Run the wizard in a terminal (tmux pane)** with `/tmp/mmg wizard`. Expected order: `== Secret store` with the Keychain sentence; `== Claude Code` "Already registered" (the real `gmail` server exists); `== OAuth client` opens page 1. Check each of the four URLs loads the page its instruction describes. On page 4 download the JSON of the existing client `multi-gmail-mac` (no new project is created); the wizard must pick it up and print "Stored OAuth client ...". Answer y to delete it. `== Accounts`: log in with one real account; check the proposed alias and description; accept. `n` for more. `== Check`: doctor lines ok. Final line present.

- [ ] **Step 3: Run it again**: every step skips; the only prompt is the account question; answer n.

- [ ] **Step 4: Clean up**: `MINUAR_MULTI_GMAIL_CONFIG_DIR=/tmp/mmg-wizard-test MINUAR_MULTI_GMAIL_KEYCHAIN_SERVICE=com.minuar.multi-gmail.wizardtest /tmp/mmg remove-account <alias>` (revokes the throwaway token at Google), `security delete-generic-password -s com.minuar.multi-gmail.wizardtest -a oauth-client-id`, same for `oauth-client-secret`, `rm -rf /tmp/mmg-wizard-test`. Confirm `minuar-multi-gmail doctor` with the real environment still reports every real account ok.

- [ ] **Step 5: Record** the outcome and any URL or wording correction; corrections go in a `fix(wizard): ...` commit.
