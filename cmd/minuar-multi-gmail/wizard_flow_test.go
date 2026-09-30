package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
)

// The secret charset excludes "@", so fake refresh tokens carry "~" for it.
// flowDeps builds a wizardDeps whose login returns the next address from
// emails on each call, with everything else faked as done.
func flowDeps(t *testing.T, input string, st secrets.Store, emails ...string) (*wizardDeps, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	i, checked := 0, 0
	dir := t.TempDir()
	d := &wizardDeps{
		in: strings.NewReader(input), out: &out,
		configDir: dir, downloads: t.TempDir(), binary: "/opt/mmg", goos: "darwin",
		detect:   func(string) (secrets.Kind, string) { return secrets.KindKeychain, "Secrets go to the macOS Keychain." },
		open:     func(secrets.Kind) (secrets.Store, error) { return st, nil },
		lookPath: func(string) (string, error) { return "/usr/local/bin/claude", nil },
		run:      func(context.Context, string, ...string) (string, error) { return "", nil },
		openURL:  func(string) error { return nil },
		login: func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: "at", RefreshToken: "rt-" + strings.ReplaceAll(emails[i], "@", "~")}, nil
		},
		profile: func(context.Context, *oauth2.Token) (string, error) {
			e := emails[i]
			i++
			return e, nil
		},
		// doctor asks once per account, in config order. The token source is
		// not called: it would refresh against the real Google endpoint.
		doctorProfile: func(context.Context, oauth2.TokenSource) (string, error) {
			cfg, err := config.Load(dir)
			if err != nil {
				return "", err
			}
			n := checked
			checked++
			return cfg.Accounts[n].Email, nil
		},
		now:   func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		sleep: func(time.Duration) {}, waitFile: time.Minute,
	}
	return d, &out
}

func TestWizardAccountsProposesAndAccepts(t *testing.T) {
	st := seededStore(t)
	// Round 1: Y, Enter (alias personal), Enter (description). Round 2: y,
	// Enter (work), typed description. Round 3: n.
	d, out := flowDeps(t, "\n\n\ny\n\nthe shop mailbox; use it for orders\nn\n", st, "ann@gmail.com", "ann@company.example")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 2 || cfg.Accounts[0].Alias != "personal" || cfg.Accounts[1].Alias != "work" || cfg.Default != "personal" {
		t.Fatalf("accounts = %+v default %q", cfg.Accounts, cfg.Default)
	}
	if cfg.Accounts[0].Description != "my personal gmail; use it when I say personal or private" || cfg.Accounts[1].Description != "the shop mailbox; use it for orders" {
		t.Fatalf("descriptions = %q, %q", cfg.Accounts[0].Description, cfg.Accounts[1].Description)
	}
	if rt, _ := st.Get(secrets.RefreshTokenKey("work")); rt != "rt-ann~company.example" {
		t.Fatalf("refresh token = %q", rt)
	}
	s := out.String()
	for _, want := range []string{"Add your first Gmail account? [Y/n]", "Alias [personal]", "When should Claude use it? [my personal gmail; use it when I say personal or private]", "Connected ann@gmail.com as \"personal\".", "Add a Gmail account? [Y/n]", "Alias [work]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("out lacks %q: %q", want, s)
		}
	}
}

func TestWizardAccountsTakenAliasGetsASuffix(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "\n\n\n"+"y\n\n\n"+"n\n", st, "ann@gmail.com", "bob@gmail.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 2 || cfg.Accounts[1].Alias != "personal2" || !strings.Contains(out.String(), "Alias [personal2]") {
		t.Fatalf("accounts = %+v out %q", cfg.Accounts, out.String())
	}
}

func TestWizardAccountsInvalidAliasReAsks(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "\nBad Alias!\nhouse\n\nn\n", st, "ann@gmail.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Alias != "house" || strings.Count(out.String(), "Alias [personal]") != 2 || !strings.Contains(out.String(), "lowercase letters") {
		t.Fatalf("accounts = %+v out %q", cfg.Accounts, out.String())
	}
}

func TestWizardAccountsDuplicateEmailIsReported(t *testing.T) {
	st := seededStore(t)
	// The duplicate is reported right after the login, before alias and description.
	d, out := flowDeps(t, "\n\n\n"+"y\n"+"n\n", st, "ann@gmail.com", "ann@gmail.com")
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 1 || !strings.Contains(out.String(), "already connected") {
		t.Fatalf("accounts = %+v out %q", cfg.Accounts, out.String())
	}
}

func TestWizardAccountsNoneAdded(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "n\n", st)
	if err := wizardAccounts(context.Background(), d, st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no account added; run the wizard again to add one") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestWizardAccountsLoginFailureStopsTheWizard(t *testing.T) {
	st := seededStore(t)
	d, _ := flowDeps(t, "\n", st, "ann@gmail.com")
	d.login = func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
		return nil, errors.New("login: timed out")
	}
	err := wizardAccounts(context.Background(), d, st)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestWizardDefault(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "", st)
	cfg := &config.File{Version: 1}
	cfg.Add(config.Account{Alias: "personal", Email: "a@gmail.com", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	if err := wizardDefault(d); err != nil || !strings.Contains(out.String(), "== Default account") || !strings.Contains(out.String(), "Only account: personal is the default.") || strings.Contains(out.String(), "Default account [") {
		t.Fatalf("one account must not ask: %v %q", err, out.String())
	}

	cfg.Add(config.Account{Alias: "work", Email: "a@company.example", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	d.in = strings.NewReader("nope\n\n")
	d.lineCh = nil
	if err := wizardDefault(d); err != nil {
		t.Fatal(err)
	}
	got, _ := config.Load(d.configDir)
	if got.Default != "work" || strings.Count(out.String(), "Default account [work]") != 2 || !strings.Contains(out.String(), "no account \"nope\"") {
		t.Fatalf("default %q out %q", got.Default, out.String())
	}

	d.in = strings.NewReader("\n")
	d.lineCh = nil
	out.Reset()
	if err := wizardDefault(d); err != nil || !strings.Contains(out.String(), "Default account [work]") {
		t.Fatalf("Enter keeps the default: %v %q", err, out.String())
	}
}

func TestRunWizardEverythingDoneSkips(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "n\n", st)
	cfg := &config.File{Version: 1, Secrets: "keychain"}
	cfg.Add(config.Account{Alias: "work", Email: "a@company.example", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	st.Set(secrets.RefreshTokenKey("work"), "rt-a~company.example")
	if err := runWizard(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"== Secret store", "recorded on an earlier run", "== Claude Code", "Already registered in Claude Code", "== OAuth client", "OAuth client stored", "== Accounts", "Add a Gmail account? [Y/n]", "== Check", "ok   secrets", "ok   work", "Restart Claude Code, then ask it about your mail."} {
		if !strings.Contains(s, want) {
			t.Fatalf("out lacks %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "[") != 1 {
		t.Fatalf("the only prompt must be the account question:\n%s", s)
	}
}

func TestRunWizardFreshRunOnDarwin(t *testing.T) {
	st := secrets.NewMem()
	// w3: Enter x3, then a pasted path, then y (delete). w4: Y, Enter,
	// Enter for a gmail account; y, Enter, Enter for a company account;
	// then n. w5: Enter accepts the proposed default.
	clientPath := writeClientFile(t, t.TempDir(), "client_secret_x.json", time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC))
	d, out := flowDeps(t, "\n\n\n"+clientPath+"\ny\n"+"\n\n\n"+"y\n\n\n"+"n\n"+"\n", st, "ann@gmail.com", "ann@company.example")
	if err := runWizard(context.Background(), d); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, _ := config.Load(d.configDir)
	if cfg.Secrets != "keychain" || len(cfg.Accounts) != 2 || cfg.Accounts[0].Alias != "personal" || cfg.Accounts[1].Alias != "work" || cfg.Default != "work" {
		t.Fatalf("config = %+v", cfg)
	}
	if _, err := os.Stat(clientPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the pasted file must be deleted after y")
	}
	if !strings.Contains(out.String(), "Default account [work]") {
		t.Fatalf("w5 must propose work:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "ok   work           ann@company.example") {
		t.Fatalf("doctor line missing:\n%s", out.String())
	}
}

func TestRunWizardFailedDoctorIsAnError(t *testing.T) {
	st := seededStore(t)
	d, out := flowDeps(t, "n\n", st)
	cfg := &config.File{Version: 1, Secrets: "keychain"}
	cfg.Add(config.Account{Alias: "work", Email: "a@company.example", AddedAt: fixedNow()})
	config.Save(d.configDir, cfg)
	st.Set(secrets.RefreshTokenKey("work"), "rt-a~company.example")
	d.doctorProfile = func(context.Context, oauth2.TokenSource) (string, error) {
		return "", errors.New("invalid_grant")
	}
	err := runWizard(context.Background(), d)
	if err == nil || !strings.Contains(err.Error(), "checks failed") || !strings.Contains(out.String(), "Some checks failed; see the FAIL lines above.") {
		t.Fatalf("err = %v out %s", err, out.String())
	}
}

func TestRunWizardFailedDoctorWithoutAccountsIsNotAnError(t *testing.T) {
	st := seededStore(t)
	d, _ := flowDeps(t, "n\n", st)
	config.Save(d.configDir, &config.File{Version: 1, Secrets: "keychain"})
	d.doctorProfile = func(context.Context, oauth2.TokenSource) (string, error) {
		return "", errors.New("never called")
	}
	if err := runWizard(context.Background(), d); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWizardLinuxRefusalWritesNothing(t *testing.T) {
	st := secrets.NewMem()
	d, _ := flowDeps(t, "n\n", st)
	d.goos = "linux"
	d.detect = func(string) (secrets.Kind, string) {
		return secrets.KindFile, "No keyring found: secret-tool is not installed."
	}
	runs := 0
	d.run = func(context.Context, string, ...string) (string, error) { runs++; return "", nil }
	err := runWizard(context.Background(), d)
	if !errors.Is(err, errStoreRefused) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(d.configDir); len(entries) != 0 || runs != 0 {
		t.Fatalf("nothing may happen after a refusal: entries %v runs %d", entries, runs)
	}
}

// failingSetStore reads from a seeded store and fails every write.
type failingSetStore struct{ secrets.Store }

func (failingSetStore) Set(string, string) error { return errors.New("disk full") }

func TestWizardAccountsStoreFailureStopsTheWizard(t *testing.T) {
	st := failingSetStore{seededStore(t)}
	d, out := flowDeps(t, "\n\n\nn\n", st, "ann@gmail.com")
	err := wizardAccounts(context.Background(), d, st)
	if err == nil || !strings.Contains(err.Error(), "store refresh token") {
		t.Fatalf("err = %v out %q", err, out.String())
	}
	cfg, _ := config.Load(d.configDir)
	if len(cfg.Accounts) != 0 {
		t.Fatalf("accounts = %+v", cfg.Accounts)
	}
}
