package googleauth

import (
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2/google"
)

const sampleClientSecret = `{"installed":{"client_id":"123-abc.apps.googleusercontent.com","project_id":"minuar-multi-gmail","auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token","client_secret":"GOCSPX-s3cr3t_Value-1","redirect_uris":["http://localhost"]}}`

func TestParseClientSecretFile(t *testing.T) {
	c, err := ParseClientSecretFile([]byte(sampleClientSecret))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "123-abc.apps.googleusercontent.com" || c.Secret != "GOCSPX-s3cr3t_Value-1" {
		t.Fatalf("creds = %+v", c)
	}
}

func TestParseClientSecretFileRejectsWebAndGarbage(t *testing.T) {
	if _, err := ParseClientSecretFile([]byte(`{"web":{"client_id":"x","client_secret":"y"}}`)); err == nil || !strings.Contains(err.Error(), "Desktop app") {
		t.Fatalf("web client accepted: %v", err)
	}
	if _, err := ParseClientSecretFile([]byte(`{"installed":{"client_id":"x"}}`)); err == nil {
		t.Fatal("missing secret accepted")
	}
	if _, err := ParseClientSecretFile([]byte(`nope`)); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestSaveAndLoadClientCreds(t *testing.T) {
	st := secrets.NewMem()
	if _, err := LoadClientCreds(st); err == nil || !strings.Contains(err.Error(), "minuar-multi-gmail setup") {
		t.Fatalf("missing creds err = %v", err)
	}
	if err := SaveClientCreds(st, ClientCreds{ID: "id.apps", Secret: "GOCSPX-x"}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadClientCreds(st)
	if err != nil || c.ID != "id.apps" || c.Secret != "GOCSPX-x" {
		t.Fatalf("creds = %+v err %v", c, err)
	}
}

func TestNewConfigDefaults(t *testing.T) {
	cfg := NewConfig(ClientCreds{ID: "id", Secret: "s"}, "http://127.0.0.1:1234/callback", google.Endpoint)
	if cfg.ClientID != "id" || cfg.ClientSecret != "s" || cfg.RedirectURL != "http://127.0.0.1:1234/callback" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != Scope {
		t.Fatalf("scopes = %v", cfg.Scopes)
	}
	if cfg.Endpoint.TokenURL != google.Endpoint.TokenURL {
		t.Fatalf("endpoint = %+v", cfg.Endpoint)
	}
}
