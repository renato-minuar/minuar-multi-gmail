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
