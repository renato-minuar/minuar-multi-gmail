package main

import (
	"errors"
	"fmt"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

var errStoreRefused = errors.New("no secret store accepted; install a keyring (see the sentence above) and run the wizard again")

// wizardStore is step w1. It returns the kind every later step uses. The
// environment variable wins over everything, then the recorded kind, then
// detection. It writes nothing: setup records the kind when the client is
// stored.
func wizardStore(d *wizardDeps) (secrets.Kind, error) {
	heading(d, "Secret store")
	if d.envKind != "" {
		kind, err := secrets.ParseKind(d.envKind)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(d.out, "Secret store forced to %s by %s.\n", kind, secrets.EnvKind)
		return kind, nil
	}
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return "", err
	}
	if cfg.Secrets != "" {
		kind, err := secrets.ParseKind(cfg.Secrets)
		if err != nil {
			return "", fmt.Errorf("recorded secret store: %w", err)
		}
		fmt.Fprintf(d.out, "Secrets go to %s (recorded on an earlier run).\n", kind.Describe(d.configDir))
		return kind, nil
	}
	kind, why := d.detect(secrets.ServiceName())
	if kind == "" {
		return "", errors.New(why)
	}
	fmt.Fprintln(d.out, why)
	if kind != secrets.KindFile || d.goos == "windows" {
		return kind, nil
	}
	yes, err := askYesNo(d, "Store tokens in a private file, readable by your user account and root?", false)
	if err != nil {
		return "", err
	}
	if !yes {
		return "", errStoreRefused
	}
	return kind, nil
}
