package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/googleauth"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
	"golang.org/x/oauth2"
)

const sampleClientSecret = `{"installed":{"client_id":"123-abc.apps.googleusercontent.com","client_secret":"GOCSPX-s3cr3t_Value-1"}}`

func fixedNow() time.Time { return time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) }

func seededStore(t *testing.T) *secrets.Mem {
	t.Helper()
	st := secrets.NewMem()
	if err := googleauth.SaveClientCreds(st, googleauth.ClientCreds{ID: "id.apps", Secret: "GOCSPX-x"}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunSetupStoresCredsAndTellsToDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client_secret_x.json")
	os.WriteFile(path, []byte(sampleClientSecret), 0o600)
	st := secrets.NewMem()
	var out bytes.Buffer
	if err := runSetup(st, path, &out); err != nil {
		t.Fatal(err)
	}
	id, _ := st.Get(secrets.ClientIDKey)
	sec, _ := st.Get(secrets.ClientSecretKey)
	if id != "123-abc.apps.googleusercontent.com" || sec != "GOCSPX-s3cr3t_Value-1" {
		t.Fatalf("stored id=%q secret=%q", id, sec)
	}
	if !strings.Contains(out.String(), "rm "+path) {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "Next: minuar-multi-gmail add-account <alias>") {
		t.Fatalf("setup must point at the next step: %q", out.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("setup must not delete the file itself")
	}
	if err := runSetup(st, filepath.Join(dir, "missing.json"), &out); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestRunAddAccountHappyPath(t *testing.T) {
	dir := t.TempDir()
	st := seededStore(t)
	loginCalls := 0
	var out bytes.Buffer
	d := addAccountDeps{
		store: st, configDir: dir, out: &out, now: fixedNow, in: strings.NewReader(""),
		login: func(ctx context.Context, c googleauth.ClientCreds) (*oauth2.Token, error) {
			loginCalls++
			if c.ID != "id.apps" {
				t.Fatalf("creds = %+v", c)
			}
			return &oauth2.Token{AccessToken: "at", RefreshToken: "1//0gRefresh-work"}, nil
		},
		profile: func(ctx context.Context, tok *oauth2.Token) (string, error) { return "control@example.com", nil },
	}
	args := addAccountArgs{alias: "work", description: "the company mailbox; use it by default", hasDesc: true}
	if err := runAddAccount(context.Background(), d, args); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(secrets.RefreshTokenKey("work")); got != "1//0gRefresh-work" {
		t.Fatalf("stored token = %q", got)
	}
	cfg, _ := config.Load(dir)
	a, ok := cfg.Find("work")
	if !ok || a.Email != "control@example.com" || cfg.Default != "work" || !a.AddedAt.Equal(fixedNow()) {
		t.Fatalf("cfg = %+v", cfg)
	}
	if a.Description != "the company mailbox; use it by default" {
		t.Fatalf("description = %q", a.Description)
	}
	if strings.Contains(out.String(), "No description set") {
		t.Fatalf("the flag path must not print the hint: %q", out.String())
	}
	// Same alias again without --replace: refused before login.
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "work"}); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("err = %v", err)
	}
	if loginCalls != 1 {
		t.Fatalf("login must not run for a refused alias; calls = %d", loginCalls)
	}
	// --replace updates token and email.
	d.login = func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
		return &oauth2.Token{RefreshToken: "1//0gRefresh-work-2"}, nil
	}
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "work", replace: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Get(secrets.RefreshTokenKey("work")); got != "1//0gRefresh-work-2" {
		t.Fatalf("stored token = %q", got)
	}
	// --replace without a new description and without a prompt keeps the old one.
	cfg, _ = config.Load(dir)
	if a, _ := cfg.Find("work"); a.Description != "the company mailbox; use it by default" {
		t.Fatalf("description after --replace = %q", a.Description)
	}
}

func TestRunAddAccountPromptsForDescription(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	d := addAccountDeps{
		store: seededStore(t), configDir: dir, out: &out, now: fixedNow,
		in: strings.NewReader("  mailbox for the shop  \n"), prompt: true,
		login: func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
			return &oauth2.Token{RefreshToken: "1//0gShop"}, nil
		},
		profile: func(context.Context, *oauth2.Token) (string, error) { return "shop@company.example", nil },
	}
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "shop"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(dir)
	a, ok := cfg.Find("shop")
	if !ok || a.Description != "mailbox for the shop" {
		t.Fatalf("saved account = %+v", a)
	}
	s := out.String()
	if !strings.Contains(s, "When should Claude use this account?") || !strings.Contains(s, "Description: ") {
		t.Fatalf("prompt missing: %q", s)
	}
	// A fresh account (no --replace, nothing stored yet) offers to skip.
	if !strings.Contains(s, "Leave empty to skip.") {
		t.Fatalf("prompt must offer to skip on a fresh account: %q", s)
	}
	if strings.Contains(s, "No description set") {
		t.Fatalf("hint printed although a description was given: %q", s)
	}

	// --replace with an existing description: the prompt offers to keep it
	// by name, and an empty answer does keep it.
	out.Reset()
	d.in = strings.NewReader("\n")
	d.login = func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
		return &oauth2.Token{RefreshToken: "1//0gShop2"}, nil
	}
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "shop", replace: true}); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	if !strings.Contains(s, `Leave empty to keep the current description: "mailbox for the shop".`) {
		t.Fatalf("replace prompt must offer to keep the existing description: %q", s)
	}
	cfg, _ = config.Load(dir)
	if a, _ := cfg.Find("shop"); a.Description != "mailbox for the shop" {
		t.Fatalf("description after an empty answer on --replace = %q, want the existing text kept", a.Description)
	}

	// A too-long answer at the prompt is rejected the same as a too-long flag.
	out.Reset()
	d.in = strings.NewReader(strings.Repeat("a", 201) + "\n")
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "shop", replace: true}); err == nil || !strings.Contains(err.Error(), "description is too long") {
		t.Fatalf("too-long prompt answer err = %v", err)
	}
}

func TestRunAddAccountWithoutPromptHintsAtSetDescription(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	d := addAccountDeps{
		store: seededStore(t), configDir: dir, out: &out, now: fixedNow,
		in: strings.NewReader(""), prompt: false,
		login: func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
			return &oauth2.Token{RefreshToken: "1//0gWork"}, nil
		},
		profile: func(context.Context, *oauth2.Token) (string, error) { return "you@company.example", nil },
	}
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "work"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(dir)
	if a, _ := cfg.Find("work"); a.Description != "" {
		t.Fatalf("description = %q, want empty", a.Description)
	}
	s := out.String()
	if strings.Contains(s, "When should Claude use this account?") {
		t.Fatalf("must not prompt when stdin is not a terminal: %q", s)
	}
	if !strings.Contains(s, "No description set. Add one with: minuar-multi-gmail set-description work") {
		t.Fatalf("hint missing: %q", s)
	}
}

func TestSetDescription(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.File{Version: 1}
	cfg.Add(config.Account{Alias: "work", Email: "you@company.example", AddedAt: fixedNow()})
	config.Save(dir, cfg)

	var out bytes.Buffer
	if err := runSetDescription(dir, "work", "the company mailbox; use it by default", &out); err != nil {
		t.Fatal(err)
	}
	saved, _ := config.Load(dir)
	if a, _ := saved.Find("work"); a.Description != "the company mailbox; use it by default" {
		t.Fatalf("description = %q", a.Description)
	}
	if !strings.Contains(out.String(), `Description for "work" set.`) {
		t.Fatalf("out = %q", out.String())
	}

	// --clear parses to an empty text, which runSetDescription clears.
	out.Reset()
	alias, text, clear, err := parseSetDescriptionArgs([]string{"work", "--clear"})
	if err != nil || alias != "work" || text != "" || !clear {
		t.Fatalf("parseSetDescriptionArgs(--clear) = %q %q %v %v", alias, text, clear, err)
	}
	if err := runSetDescription(dir, alias, text, &out); err != nil {
		t.Fatal(err)
	}
	saved, _ = config.Load(dir)
	if a, _ := saved.Find("work"); a.Description != "" {
		t.Fatalf("description = %q, want cleared", a.Description)
	}
	if !strings.Contains(out.String(), `Description for "work" cleared.`) {
		t.Fatalf("out = %q", out.String())
	}

	// No text and no --clear is a usage error: nothing is loaded or changed.
	wantUsage := "usage: minuar-multi-gmail set-description <alias> <text> | set-description <alias> --clear"
	if _, _, _, err := parseSetDescriptionArgs([]string{"work"}); err == nil || err.Error() != wantUsage {
		t.Fatalf("bare alias err = %v, want %q", err, wantUsage)
	}
	if _, _, _, err := parseSetDescriptionArgs(nil); err == nil {
		t.Fatal("missing alias accepted")
	}

	if err := runSetDescription(dir, "ghost", "x", &out); err == nil {
		t.Fatal("unknown alias accepted")
	}

	tooLong := strings.Repeat("a", 201)
	if err := runSetDescription(dir, "work", tooLong, &out); err == nil || !strings.Contains(err.Error(), "description is too long") {
		t.Fatalf("err = %v", err)
	}
	saved, _ = config.Load(dir)
	if a, _ := saved.Find("work"); a.Description != "" {
		t.Fatalf("description changed by a rejected update: %q", a.Description)
	}
}

func TestRunAddAccountRefusesDuplicateEmailWithoutStoringToken(t *testing.T) {
	dir := t.TempDir()
	st := seededStore(t)
	d := addAccountDeps{
		store: st, configDir: dir, out: &bytes.Buffer{}, now: fixedNow, in: strings.NewReader(""),
		login: func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
			return &oauth2.Token{RefreshToken: "1//0gA"}, nil
		},
		profile: func(context.Context, *oauth2.Token) (string, error) { return "control@example.com", nil },
	}
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "work"}); err != nil {
		t.Fatal(err)
	}
	err := runAddAccount(context.Background(), d, addAccountArgs{alias: "again"})
	if err == nil || !strings.Contains(err.Error(), `already connected as alias "work"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := st.Get(secrets.RefreshTokenKey("again")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("token for the refused alias must not be stored")
	}
}

func TestRunAddAccountValidation(t *testing.T) {
	d := addAccountDeps{store: seededStore(t), configDir: t.TempDir(), out: &bytes.Buffer{}, now: fixedNow,
		login: func(context.Context, googleauth.ClientCreds) (*oauth2.Token, error) {
			t.Fatal("must not login")
			return nil, nil
		},
		profile: func(context.Context, *oauth2.Token) (string, error) { return "", nil }}
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "Bad Alias"}); err == nil {
		t.Fatal("invalid alias accepted")
	}
	tooLong := addAccountArgs{alias: "work", description: strings.Repeat("a", 201), hasDesc: true}
	if err := runAddAccount(context.Background(), d, tooLong); err == nil || !strings.Contains(err.Error(), "description is too long") {
		t.Fatalf("too-long --description err = %v, want it rejected before login", err)
	}
	d.store = secrets.NewMem()
	if err := runAddAccount(context.Background(), d, addAccountArgs{alias: "work"}); err == nil || !strings.Contains(err.Error(), "minuar-multi-gmail setup") {
		t.Fatalf("missing creds err = %v", err)
	}
}

func TestAccountsSetDefaultRemove(t *testing.T) {
	dir := t.TempDir()
	st := seededStore(t)
	cfg := &config.File{Version: 1}
	cfg.Add(config.Account{Alias: "work", Email: "control@example.com", AddedAt: fixedNow()})
	cfg.Add(config.Account{Alias: "personal", Email: "p@gmail.com", AddedAt: fixedNow()})
	cfg.SetDescription("personal", "the personal mailbox")
	config.Save(dir, cfg)
	st.Set(secrets.RefreshTokenKey("personal"), "1//0gPersonal")

	var out bytes.Buffer
	if err := runAccounts(dir, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "p@gmail.com") || !strings.Contains(out.String(), "(default)") {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "the personal mailbox") {
		t.Fatalf("accounts must list the description: %q", out.String())
	}
	out.Reset()
	if err := runSetDefault(dir, "personal", &out); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(dir); cfg.Default != "personal" {
		t.Fatalf("default = %q", cfg.Default)
	}
	if err := runSetDefault(dir, "nope", &out); err == nil {
		t.Fatal("unknown alias accepted")
	}

	var revoked []string
	revoke := func(_ context.Context, tok string) error {
		revoked = append(revoked, tok)
		return errors.New("google down")
	}
	out.Reset()
	if err := runRemoveAccount(context.Background(), st, dir, "personal", revoke, &out); err != nil {
		t.Fatalf("revoke failure must be best effort: %v", err)
	}
	if strings.Join(revoked, ",") != "1//0gPersonal" || !strings.Contains(out.String(), "revoke") {
		t.Fatalf("revoked = %v out = %q", revoked, out.String())
	}
	if _, err := st.Get(secrets.RefreshTokenKey("personal")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("token must be deleted")
	}
	after, _ := config.Load(dir)
	if _, ok := after.Find("personal"); ok || after.Default != "" {
		t.Fatalf("cfg after remove = %+v", after)
	}
	if err := runRemoveAccount(context.Background(), st, dir, "ghost", revoke, &out); err == nil {
		t.Fatal("unknown alias accepted")
	}
}

func TestRunDoctor(t *testing.T) {
	dir := t.TempDir()
	st := seededStore(t)
	cfg := &config.File{Version: 1}
	cfg.Add(config.Account{Alias: "work", Email: "control@example.com", AddedAt: fixedNow()})
	cfg.Add(config.Account{Alias: "personal", Email: "p@gmail.com", AddedAt: fixedNow()})
	config.Save(dir, cfg)
	st.Set(secrets.RefreshTokenKey("work"), "1//0gWork")
	// personal has no stored token on purpose

	var out bytes.Buffer
	ok := runDoctor(context.Background(), doctorDeps{store: st, configDir: dir, out: &out,
		profile: func(context.Context, oauth2.TokenSource) (string, error) { return "control@example.com", nil }})
	if ok {
		t.Fatal("doctor must fail when one account has no token")
	}
	s := out.String()
	if !strings.Contains(s, "ok") || !strings.Contains(s, "work") || !strings.Contains(s, "FAIL") || !strings.Contains(s, "personal") || !strings.Contains(s, "needs re-login") {
		t.Fatalf("out = %q", s)
	}

	st.Set(secrets.RefreshTokenKey("personal"), "1//0gPersonal")
	out.Reset()
	ok = runDoctor(context.Background(), doctorDeps{store: st, configDir: dir, out: &out,
		profile: func(context.Context, oauth2.TokenSource) (string, error) { return "control@example.com", nil }})
	if ok || !strings.Contains(out.String(), "expected p@gmail.com") {
		t.Fatalf("email mismatch must fail: ok=%v out=%q", ok, out.String())
	}

	out.Reset()
	calls := 0
	ok = runDoctor(context.Background(), doctorDeps{store: st, configDir: dir, out: &out,
		profile: func(context.Context, oauth2.TokenSource) (string, error) {
			calls++
			if calls == 1 {
				return "control@example.com", nil
			}
			return "p@gmail.com", nil
		}})
	if !ok {
		t.Fatalf("expected healthy: %q", out.String())
	}

	out.Reset()
	if runDoctor(context.Background(), doctorDeps{store: secrets.NewMem(), configDir: dir, out: &out, profile: nil}) {
		t.Fatal("missing client creds must fail")
	}
}

func TestProviderCachesPerAlias(t *testing.T) {
	dir := t.TempDir()
	st := seededStore(t)
	cfg := &config.File{Version: 1}
	cfg.Add(config.Account{Alias: "work", Email: "control@example.com", AddedAt: fixedNow()})
	config.Save(dir, cfg)
	st.Set(secrets.RefreshTokenKey("work"), "1//0gWork")
	p := newProvider(st, dir)
	a, err := p.service(context.Background(), "work")
	if err != nil || a.Email() != "control@example.com" {
		t.Fatalf("service = %v err %v", a, err)
	}
	b, _ := p.service(context.Background(), "work")
	if a != b {
		t.Fatal("service must be cached per alias")
	}
	if _, err := p.service(context.Background(), "ghost"); err == nil {
		t.Fatal("unknown alias accepted")
	}
	st.Delete(secrets.RefreshTokenKey("work"))
	p2 := newProvider(st, dir)
	_, err = p2.service(context.Background(), "work")
	var re *googleauth.ReauthError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want ReauthError", err)
	}
}

func TestAddAccountArgParsing(t *testing.T) {
	got, err := parseAddAccountArgs([]string{"work", "--replace"})
	if err != nil || got.alias != "work" || !got.replace || got.hasDesc {
		t.Fatalf("got %+v %v", got, err)
	}
	got, err = parseAddAccountArgs([]string{"-replace", "personal"})
	if err != nil || got.alias != "personal" || !got.replace {
		t.Fatalf("got %+v %v", got, err)
	}
	got, err = parseAddAccountArgs([]string{"personal", "--description", "  the personal mailbox  "})
	if err != nil || got.alias != "personal" || !got.hasDesc || got.description != "the personal mailbox" {
		t.Fatalf("got %+v %v", got, err)
	}
	got, err = parseAddAccountArgs([]string{"personal", "--description", ""})
	if err != nil || !got.hasDesc || got.description != "" {
		t.Fatalf("an explicit empty description must be kept: %+v %v", got, err)
	}
	if _, err := parseAddAccountArgs([]string{"work", "--description"}); err == nil || err.Error() != "--description needs a text value" {
		t.Fatalf("--description without a text: err = %v", err)
	}
	if _, err := parseAddAccountArgs([]string{"work", "--description", "--replace"}); err == nil || err.Error() != "--description needs a text value" {
		t.Fatalf("--description with a flag-like value: err = %v", err)
	}
	if _, err := parseAddAccountArgs(nil); err == nil {
		t.Fatal("missing alias accepted")
	}
	if _, err := parseAddAccountArgs([]string{"a", "b"}); err == nil {
		t.Fatal("two aliases accepted")
	}
	if _, err := parseAddAccountArgs([]string{"work", "--bogus"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestServeDepsWiring(t *testing.T) {
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })

	dir := t.TempDir()
	st := seededStore(t)
	p := newProvider(st, dir)
	var errBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&errBuf, nil))

	d := serveDeps(p, logger)
	if d.Config == nil || d.Service == nil || d.Logger == nil || d.Now == nil {
		t.Fatalf("deps = %+v", d)
	}
	if d.Logger != logger {
		t.Fatal("deps logger must be the injected logger")
	}
	if slog.Default() != logger {
		t.Fatal("slog.Default() must be the injected logger after serveDeps")
	}

	server := buildServer(p, logger, &errBuf)
	if server == nil {
		t.Fatal("buildServer returned nil")
	}
}

func TestServerInstructions(t *testing.T) {
	cfg := &config.File{Version: 1, Default: "work", Accounts: []config.Account{
		{Alias: "work", Email: "you@company.example", Description: "the company mailbox; use it by default"},
		{Alias: "personal", Email: "you@gmail.com"},
	}}
	got := serverInstructions(cfg)
	head := "Gmail for 2 accounts. The default account is 'work' (you@company.example). " +
		"'work' (you@company.example): the company mailbox; use it by default. " +
		"'personal' (you@gmail.com): use it when the user names this alias or address. "
	if !strings.HasPrefix(got, head) {
		t.Fatalf("instructions = %q, want prefix %q", got, head)
	}
	for _, want := range []string{
		"Pick the account from the user's wording and never ask which one.",
		"Mail is sent by send_draft, forward_message and forward_messages; create_draft never sends.",
		"Use forward_messages when more than one message is to be forwarded: one call, one approval.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instructions lack %q: %q", want, got)
		}
	}
	if strings.Contains(got, "house") {
		t.Fatalf("instructions must carry no hardcoded account data: %q", got)
	}

	one := &config.File{Version: 1, Default: "work", Accounts: []config.Account{{Alias: "work", Email: "you@company.example"}}}
	if !strings.HasPrefix(serverInstructions(one), "Gmail for 1 account. ") {
		t.Fatalf("single account instructions = %q", serverInstructions(one))
	}

	empty := serverInstructions(&config.File{Version: 1})
	if !strings.HasPrefix(empty, "No Gmail accounts are configured. Run minuar-multi-gmail add-account <alias> first. ") {
		t.Fatalf("empty instructions = %q", empty)
	}
	if !strings.Contains(empty, "create_draft never sends") {
		t.Fatalf("empty instructions must keep the tool guidance: %q", empty)
	}
}

func TestCommandsRegistered(t *testing.T) {
	for _, name := range []string{"setup", "add-account", "remove-account", "set-default", "set-description", "accounts", "doctor", "serve", "version"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("command %q not registered", name)
		}
		if _, ok := commandHelp[name]; !ok {
			t.Errorf("command %q has no help line", name)
		}
	}
}
