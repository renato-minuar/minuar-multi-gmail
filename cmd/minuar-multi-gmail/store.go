package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// openStore opens the secret store every command uses: the kind forced by
// MINUAR_MULTI_GMAIL_SECRETS, else the kind setup recorded in
// accounts.json. It never probes the system; only setup does that, so the
// store does not depend on how the process was started.
func openStore(configDir string) (secrets.Kind, secrets.Store, error) {
	return openStoreOn(configDir, runtime.GOOS, os.Getenv(secrets.EnvKind))
}

func openStoreOn(configDir, goos, envKind string) (secrets.Kind, secrets.Store, error) {
	var kind secrets.Kind
	if envKind != "" {
		k, err := secrets.ParseKind(envKind)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", secrets.EnvKind, err)
		}
		kind = k
	} else {
		cfg, err := config.Load(configDir)
		if err != nil {
			return "", nil, fmt.Errorf("load accounts: %w", err)
		}
		kind = secrets.Kind(cfg.Secrets)
	}
	if kind == "" {
		// Files written before the field existed: every such install is a
		// Mac using the Keychain.
		if goos != "darwin" {
			return "", nil, errors.New("no secret store recorded: run minuar-multi-gmail setup first")
		}
		kind = secrets.KindKeychain
	}
	st, err := secrets.OpenOn(kind, secrets.ServiceName(), configDir, goos)
	if err != nil {
		return "", nil, err
	}
	return kind, st, nil
}

// failingStore answers every call with one error. serve uses it when the
// store cannot be opened, so the server still starts and each tool call
// carries the message instead of Claude Code showing a dead server.
type failingStore struct{ err error }

func (f failingStore) Get(string) (string, error) { return "", f.err }
func (f failingStore) Set(string, string) error   { return f.err }
func (f failingStore) Delete(string) error        { return f.err }
