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
