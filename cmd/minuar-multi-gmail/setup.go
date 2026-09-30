package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// errFileStoreNotAccepted is returned when the only store is the file and
// the user has not said yes to it. Windows is exempt: there is no other
// store there in this version.
var errFileStoreNotAccepted = errors.New("the file store keeps tokens readable by anyone with your user account or root. " +
	"Set " + secrets.EnvKind + "=file to accept it, or install a keyring and run setup again")

type setupDeps struct {
	configDir string
	goos      string
	envKind   string
	detect    func(service string) (secrets.Kind, string)
	open      func(kind secrets.Kind) (secrets.Store, error)
}

func init() {
	commandHelp["setup"] = "store the OAuth client from a downloaded client_secret.json in the secret store this system offers"
	commands["setup"] = func(_ context.Context, args []string, io stdio) int {
		if len(args) != 1 {
			fmt.Fprintln(io.err, "usage: minuar-multi-gmail setup <path/to/client_secret.json>")
			return 2
		}
		dir, err := config.Dir()
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		d := setupDeps{
			configDir: dir, goos: runtime.GOOS, envKind: os.Getenv(secrets.EnvKind),
			detect: secrets.Detect,
			open: func(kind secrets.Kind) (secrets.Store, error) {
				return secrets.Open(kind, secrets.ServiceName(), dir)
			},
		}
		if err := runSetup(d, args[0], io.out); err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
}

// runSetup is the one place a secret store is chosen. It prints the
// reason, refuses the file store on Linux until the user accepts it,
// stores the OAuth client, and records the kind in accounts.json so every
// later command opens the same store.
func runSetup(d setupDeps, path string, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	creds, err := googleauth.ParseClientSecretFile(data)
	if err != nil {
		return err
	}
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	kind, why := d.detect(secrets.ServiceName())
	if kind == "" {
		return errors.New(why)
	}
	fmt.Fprintln(out, why)
	// A recorded file store means the user accepted it on an earlier run.
	accepted := d.envKind != "" || cfg.Secrets == string(secrets.KindFile)
	if kind == secrets.KindFile && d.goos != "windows" && !accepted {
		return errFileStoreNotAccepted
	}
	oldKind := cfg.Secrets
	if oldKind == "" && d.goos == "darwin" {
		oldKind = string(secrets.KindKeychain)
	}
	if oldKind != "" && oldKind != string(kind) && len(cfg.Accounts) > 0 {
		fmt.Fprintf(out, "The secret store changes from %s to %s. These accounts need a new login:\n", oldKind, kind)
		for _, a := range cfg.Accounts {
			fmt.Fprintf(out, "  minuar-multi-gmail add-account %s --replace\n", a.Alias)
		}
		if oldKind == string(secrets.KindFile) {
			fmt.Fprintf(out, "Then delete %s; it still holds their old tokens.\n", filepath.Join(d.configDir, secrets.FileName))
		}
	}
	store, err := d.open(kind)
	if err != nil {
		return err
	}
	if err := storeClient(d.configDir, kind, store, cfg, creds, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "Delete the downloaded file now:\n  rm %s\nNext: minuar-multi-gmail add-account <alias>\n", path)
	return nil
}

// storeClient saves the OAuth client in store and records kind in
// accounts.json. It is the last half of setup and the wizard's way to
// store a file it found by itself.
func storeClient(configDir string, kind secrets.Kind, store secrets.Store, cfg *config.File, creds googleauth.ClientCreds, out io.Writer) error {
	if err := googleauth.SaveClientCreds(store, creds); err != nil {
		return fmt.Errorf("store in %s: %w", kind.Describe(configDir), err)
	}
	cfg.Secrets = string(kind)
	if err := config.Save(configDir, cfg); err != nil {
		return fmt.Errorf("credentials are stored in %s but not recorded in accounts.json: %w; fix the file and run setup again", kind.Describe(configDir), err)
	}
	fmt.Fprintf(out, "Stored OAuth client %s in the %s.\n", creds.ID, kind.Describe(configDir))
	return nil
}
