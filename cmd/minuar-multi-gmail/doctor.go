package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/gmail"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

type doctorDeps struct {
	store     secrets.Store
	storeKind secrets.Kind
	configDir string
	profile   func(ctx context.Context, ts oauth2.TokenSource) (string, error)
	out       io.Writer
}

func init() {
	commandHelp["doctor"] = "check the secret store, config and every account's token"
	commands["doctor"] = func(ctx context.Context, _ []string, io stdio) int {
		dir, err := config.Dir()
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		kind, store, err := openStore(dir)
		if err != nil {
			fmt.Fprintf(io.out, "FAIL %-14s %s\n", "secrets", err)
			return 1
		}
		ok := runDoctor(ctx, doctorDeps{
			store: store, storeKind: kind, configDir: dir, out: io.out,
			profile: func(ctx context.Context, ts oauth2.TokenSource) (string, error) {
				return gmail.ProfileEmail(ctx, option.WithTokenSource(ts))
			},
		})
		if !ok {
			return 1
		}
		return 0
	}
}

// runDoctor prints one line per check and returns true only when all pass.
func runDoctor(ctx context.Context, d doctorDeps) bool {
	healthy := true
	report := func(ok bool, what, detail string) {
		status := "ok  "
		if !ok {
			status = "FAIL"
			healthy = false
		}
		fmt.Fprintf(d.out, "%s %-14s %s\n", status, what, detail)
	}
	report(true, "secrets", string(d.storeKind))
	creds, err := googleauth.LoadClientCreds(d.store)
	if err != nil {
		report(false, "oauth-client", err.Error())
		return false
	}
	report(true, "oauth-client", creds.ID)
	cfg, err := config.Load(d.configDir)
	if err != nil {
		report(false, "config", err.Error())
		return false
	}
	report(len(cfg.Accounts) > 0, "config", fmt.Sprintf("%d account(s), default %q", len(cfg.Accounts), cfg.Default))
	if len(cfg.Accounts) > 1 && cfg.Default == "" {
		report(false, "default", "no default account: run minuar-multi-gmail set-default <alias>")
	}
	for _, a := range cfg.Accounts {
		ts, err := googleauth.NewTokenSource(context.Background(), googleauth.TokenSourceOptions{
			Creds: creds, Store: d.store, Alias: a.Alias, Email: a.Email,
		})
		if err != nil {
			report(false, a.Alias, err.Error())
			continue
		}
		email, err := d.profile(ctx, ts)
		if err != nil {
			report(false, a.Alias, err.Error())
			continue
		}
		if !strings.EqualFold(email, a.Email) {
			report(false, a.Alias, fmt.Sprintf("token belongs to %s, expected %s", email, a.Email))
			continue
		}
		report(true, a.Alias, a.Email)
	}
	return healthy
}
