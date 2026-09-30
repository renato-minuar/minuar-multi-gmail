package main

import (
	"context"
	"fmt"
	"strings"

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
		duplicate := false
		for _, a := range cfg.Accounts {
			if strings.EqualFold(a.Email, email) {
				fmt.Fprintf(d.out, "Not added: %s is already connected as alias %q\n", email, a.Alias)
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
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
		// saveAccount fails on the store or the disk; cfg.Add also rejects a
		// taken alias or duplicate address, but both were ruled out above.
		// Every error left is fatal, so it is returned as is.
		if err := saveAccount(d.configDir, store, cfg, acct, tok, false); err != nil {
			return err
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
