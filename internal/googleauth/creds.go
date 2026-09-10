// Package googleauth owns the OAuth client credentials, the one-time
// browser login, and the per-account token source used at runtime.
package googleauth

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	Scope     = "https://www.googleapis.com/auth/gmail.modify"
	RevokeURL = "https://oauth2.googleapis.com/revoke"
)

type ClientCreds struct {
	ID     string
	Secret string
}

// ParseClientSecretFile reads the JSON that Google Cloud downloads for an
// OAuth client of type "Desktop app" (top-level key "installed").
func ParseClientSecretFile(data []byte) (ClientCreds, error) {
	var doc struct {
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ClientCreds{}, fmt.Errorf("parse client secret file: %w", err)
	}
	if doc.Installed == nil {
		return ClientCreds{}, errors.New("client secret file has no \"installed\" section: the OAuth client must be of type Desktop app")
	}
	if doc.Installed.ClientID == "" || doc.Installed.ClientSecret == "" {
		return ClientCreds{}, errors.New("client secret file is missing client_id or client_secret")
	}
	return ClientCreds{ID: doc.Installed.ClientID, Secret: doc.Installed.ClientSecret}, nil
}

func LoadClientCreds(store secrets.Store) (ClientCreds, error) {
	id, err := store.Get(secrets.ClientIDKey)
	if err != nil {
		return ClientCreds{}, fmt.Errorf("OAuth client not configured (%w): run minuar-multi-gmail setup <client_secret.json>", err)
	}
	secret, err := store.Get(secrets.ClientSecretKey)
	if err != nil {
		return ClientCreds{}, fmt.Errorf("OAuth client not configured (%w): run minuar-multi-gmail setup <client_secret.json>", err)
	}
	return ClientCreds{ID: id, Secret: secret}, nil
}

func SaveClientCreds(store secrets.Store, c ClientCreds) error {
	if !secrets.ValidSecret(c.ID) || !secrets.ValidSecret(c.Secret) {
		return errors.New("client id or secret contains unexpected characters")
	}
	if err := store.Set(secrets.ClientIDKey, c.ID); err != nil {
		return err
	}
	return store.Set(secrets.ClientSecretKey, c.Secret)
}

// NewConfig builds the oauth2 config. A zero endpoint means Google.
func NewConfig(c ClientCreds, redirectURL string, ep oauth2.Endpoint) *oauth2.Config {
	if ep.TokenURL == "" {
		ep = google.Endpoint
	}
	return &oauth2.Config{
		ClientID:     c.ID,
		ClientSecret: c.Secret,
		Endpoint:     ep,
		RedirectURL:  redirectURL,
		Scopes:       []string{Scope},
	}
}
