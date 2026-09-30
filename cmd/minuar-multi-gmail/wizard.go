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
