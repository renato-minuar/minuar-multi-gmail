package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

func init() {
	commandHelp["setup"] = "store the OAuth client from a downloaded client_secret.json in the Keychain"
	commands["setup"] = func(_ context.Context, args []string, io stdio) int {
		if len(args) != 1 {
			fmt.Fprintln(io.err, "usage: minuar-multi-gmail setup <path/to/client_secret.json>")
			return 2
		}
		if err := runSetup(secrets.NewKeychain(secrets.ServiceName()), args[0], io.out); err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
		return 0
	}
}

func runSetup(store secrets.Store, path string, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	creds, err := googleauth.ParseClientSecretFile(data)
	if err != nil {
		return err
	}
	if err := googleauth.SaveClientCreds(store, creds); err != nil {
		return fmt.Errorf("store in Keychain: %w", err)
	}
	fmt.Fprintf(out, "Stored OAuth client %s in the Keychain.\nDelete the downloaded file now:\n  rm %s\nNext: minuar-multi-gmail add-account <alias>\n", creds.ID, path)
	return nil
}
