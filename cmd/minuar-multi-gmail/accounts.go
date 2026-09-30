package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// setDescriptionUsage is both the closure's parse-error text and the
// argument-count usage line: set-description takes new text, or --clear,
// never neither.
const setDescriptionUsage = "usage: minuar-multi-gmail set-description <alias> <text> | set-description <alias> --clear"

// parseSetDescriptionArgs interprets set-description's arguments: an alias
// plus either the new text or --clear. Neither given is a usage error, not
// a silent clear.
func parseSetDescriptionArgs(args []string) (alias, text string, clear bool, err error) {
	if len(args) < 1 {
		return "", "", false, errors.New(setDescriptionUsage)
	}
	alias = args[0]
	rest := args[1:]
	switch {
	case len(rest) == 1 && rest[0] == "--clear":
		return alias, "", true, nil
	case len(rest) == 0:
		return "", "", false, errors.New(setDescriptionUsage)
	default:
		return alias, strings.Join(rest, " "), false, nil
	}
}

func init() {
	commandHelp["accounts"] = "list connected accounts"
	commandHelp["set-default"] = "set the default account alias"
	commandHelp["set-description"] = "set the sentence that tells Claude when to use an account (--clear to remove it)"
	commandHelp["remove-account"] = "revoke and forget an account"

	commands["accounts"] = func(_ context.Context, args []string, io stdio) int {
		dir, err := config.Dir()
		if err == nil {
			err = runAccounts(dir, io.out)
		}
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
	commands["set-default"] = func(_ context.Context, args []string, io stdio) int {
		if len(args) != 1 {
			fmt.Fprintln(io.err, "usage: minuar-multi-gmail set-default <alias>")
			return 2
		}
		dir, err := config.Dir()
		if err == nil {
			err = runSetDefault(dir, args[0], io.out)
		}
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
	commands["set-description"] = func(_ context.Context, args []string, io stdio) int {
		alias, text, _, err := parseSetDescriptionArgs(args)
		if err != nil {
			fmt.Fprintln(io.err, err)
			return 2
		}
		dir, err := config.Dir()
		if err == nil {
			err = runSetDescription(dir, alias, text, io.out)
		}
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
	commands["remove-account"] = func(ctx context.Context, args []string, io stdio) int {
		if len(args) != 1 {
			fmt.Fprintln(io.err, "usage: minuar-multi-gmail remove-account <alias>")
			return 2
		}
		dir, err := config.Dir()
		var store secrets.Store
		if err == nil {
			_, store, err = openStore(dir)
		}
		if err == nil {
			revoke := func(ctx context.Context, tok string) error { return googleauth.Revoke(ctx, "", tok) }
			err = runRemoveAccount(ctx, store, dir, args[0], revoke, io.out)
		}
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
}

func runAccounts(configDir string, out io.Writer) error {
	cfg, err := config.Load(configDir)
	if err != nil {
		return err
	}
	if len(cfg.Accounts) == 0 {
		fmt.Fprintln(out, "no accounts connected: run minuar-multi-gmail add-account <alias>")
		return nil
	}
	for _, a := range cfg.Accounts {
		mark := ""
		if a.Alias == cfg.Default {
			mark = "(default)"
		}
		line := fmt.Sprintf("%-12s %-30s %-10s %s", a.Alias, a.Email, mark, a.Description)
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	return nil
}

func runSetDescription(configDir, alias, text string, out io.Writer) error {
	cfg, err := config.Load(configDir)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if err := config.ValidDescription(text); err != nil {
		return err
	}
	if err := cfg.SetDescription(alias, text); err != nil {
		return err
	}
	if err := config.Save(configDir, cfg); err != nil {
		return err
	}
	if text == "" {
		fmt.Fprintf(out, "Description for %q cleared.\n", alias)
		return nil
	}
	fmt.Fprintf(out, "Description for %q set.\n", alias)
	return nil
}

func runSetDefault(configDir, alias string, out io.Writer) error {
	cfg, err := config.Load(configDir)
	if err != nil {
		return err
	}
	if err := cfg.SetDefault(alias); err != nil {
		return err
	}
	if err := config.Save(configDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "default account is now %q\n", alias)
	return nil
}

func runRemoveAccount(ctx context.Context, store secrets.Store, configDir, alias string, revoke func(ctx context.Context, token string) error, out io.Writer) error {
	cfg, err := config.Load(configDir)
	if err != nil {
		return err
	}
	if _, ok := cfg.Find(alias); !ok {
		return fmt.Errorf("alias %q does not exist", alias)
	}
	key := secrets.RefreshTokenKey(alias)
	tok, err := store.Get(key)
	switch {
	case err == nil:
		if rerr := revoke(ctx, tok); rerr != nil {
			fmt.Fprintf(out, "warning: revoke at Google failed (%v); the token is still deleted locally\n", rerr)
		}
		if derr := store.Delete(key); derr != nil && !errors.Is(derr, secrets.ErrNotFound) {
			return fmt.Errorf("delete token: %w", derr)
		}
	case errors.Is(err, secrets.ErrNotFound):
		fmt.Fprintln(out, "no stored token for this alias; removing the config entry only")
	default:
		return fmt.Errorf("read token: %w", err)
	}
	if err := cfg.Remove(alias); err != nil {
		return err
	}
	if err := config.Save(configDir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed account %q\n", alias)
	if cfg.Default == "" && len(cfg.Accounts) > 0 {
		fmt.Fprintln(out, "no default account set: run minuar-multi-gmail set-default <alias>")
	}
	return nil
}
