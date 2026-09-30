package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// descriptionPromptText builds the prompt asking for the sentence the model
// reads to pick this account. It is shown only when stdin is a terminal.
// existing is the description already on file (set only on --replace, when
// one was stored); an empty answer then keeps it instead of skipping.
func descriptionPromptText(existing string) string {
	last := "Leave empty to skip."
	if existing != "" {
		last = fmt.Sprintf("Leave empty to keep the current description: %q.", existing)
	}
	return fmt.Sprintf(`When should Claude use this account? One sentence, for example: "the user's personal gmail.com mailbox; use it when they say personal or private". %s`, last)
}

type addAccountDeps struct {
	store     secrets.Store
	configDir string
	login     func(ctx context.Context, creds googleauth.ClientCreds) (*oauth2.Token, error)
	profile   func(ctx context.Context, tok *oauth2.Token) (string, error)
	out       io.Writer
	in        io.Reader
	// prompt is true when in is a terminal: only then does add-account ask
	// for a description instead of reading a pipe that has no answer.
	prompt bool
	now    func() time.Time
}

// addAccountArgs is the parsed command line. hasDesc separates "--description
// was given and is empty" (clear it) from "no flag" (ask, or keep).
type addAccountArgs struct {
	alias       string
	replace     bool
	description string
	hasDesc     bool
}

// stdinIsTerminal reports whether stdin is a terminal. A var so tests can
// pin the answer instead of depending on how `go test` was started.
var stdinIsTerminal = func() bool { return isTerminalFd(os.Stdin.Fd()) }

func parseAddAccountArgs(args []string) (addAccountArgs, error) {
	var parsed addAccountArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--replace" || a == "-replace":
			parsed.replace = true
		case a == "--description" || a == "-description":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return addAccountArgs{}, errors.New("--description needs a text value")
			}
			i++
			parsed.description = strings.TrimSpace(args[i])
			parsed.hasDesc = true
		case strings.HasPrefix(a, "-"):
			return addAccountArgs{}, fmt.Errorf("unknown flag %q", a)
		case parsed.alias != "":
			return addAccountArgs{}, errors.New("exactly one alias expected")
		default:
			parsed.alias = a
		}
	}
	if parsed.alias == "" {
		return addAccountArgs{}, errors.New("usage: minuar-multi-gmail add-account <alias> [--replace] [--description <text>]")
	}
	return parsed, nil
}

// askDescription prints the prompt and reads one line from in. existing is
// the description already on file, passed straight to descriptionPromptText.
func askDescription(in io.Reader, out io.Writer, existing string) (string, error) {
	fmt.Fprintln(out, descriptionPromptText(existing))
	fmt.Fprint(out, "Description: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read description: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func init() {
	commandHelp["add-account"] = "log in a Google account in the browser and store it under an alias (--replace to re-login, --description <text> to say when to use it)"
	commands["add-account"] = func(ctx context.Context, args []string, io stdio) int {
		parsed, err := parseAddAccountArgs(args)
		if err != nil {
			fmt.Fprintln(io.err, err)
			return 2
		}
		dir, err := config.Dir()
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		_, store, err := openStore(dir)
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		d := addAccountDeps{
			store: store, configDir: dir, out: io.out, now: time.Now,
			in: os.Stdin, prompt: stdinIsTerminal(),
			login: func(ctx context.Context, creds googleauth.ClientCreds) (*oauth2.Token, error) {
				return googleauth.Login(ctx, googleauth.LoginOptions{Creds: creds, OpenURL: openBrowser, Notify: io.out})
			},
			profile: func(ctx context.Context, tok *oauth2.Token) (string, error) {
				return gmail.ProfileEmail(ctx, option.WithTokenSource(oauth2.StaticTokenSource(tok)))
			},
		}
		if err := runAddAccount(ctx, d, parsed); err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
}

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
	creds, err := googleauth.LoadClientCreds(d.store)
	if err != nil {
		return err
	}
	tok, err := d.login(ctx, creds)
	if err != nil {
		return err
	}
	email, err := d.profile(ctx, tok)
	if err != nil {
		return fmt.Errorf("read account email: %w", err)
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
	if replace {
		err = cfg.Replace(acct)
	} else {
		err = cfg.Add(acct)
	}
	if err != nil {
		return err
	}
	key := secrets.RefreshTokenKey(alias)
	if err := d.store.Set(key, tok.RefreshToken); err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	if err := config.Save(d.configDir, cfg); err != nil {
		d.store.Delete(key)
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
