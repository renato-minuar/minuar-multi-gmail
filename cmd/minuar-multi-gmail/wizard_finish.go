package main

import (
	"context"
	"fmt"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// wizardDefault is step w5: with several accounts, confirm or change the
// default. One account is the default already; the step says so.
func wizardDefault(d *wizardDeps) error {
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	heading(d, "Default account")
	switch len(cfg.Accounts) {
	case 0:
		fmt.Fprintln(d.out, "No account yet.")
		return nil
	case 1:
		fmt.Fprintf(d.out, "Only account: %s is the default.\n", cfg.Accounts[0].Alias)
		return nil
	}
	proposal := cfg.Default
	if _, ok := cfg.Find("work"); ok {
		proposal = "work"
	}
	for {
		alias, err := ask(d, "Default account", proposal)
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
func wizardCheck(ctx context.Context, d *wizardDeps, store secrets.Store, kind secrets.Kind) bool {
	heading(d, "Check")
	ok := runDoctor(ctx, doctorDeps{store: store, storeKind: kind, configDir: d.configDir, profile: d.doctorProfile, out: d.out})
	fmt.Fprintln(d.out, "\nRestart Claude Code, then ask it about your mail.")
	return ok
}
