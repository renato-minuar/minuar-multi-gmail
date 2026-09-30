package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func acct(alias, email string) Account {
	return Account{Alias: alias, Email: email, AddedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
}

func TestValidAlias(t *testing.T) {
	good := []string{"work", "personal", "a", "w0rk-2", strings.Repeat("a", 32)}
	bad := []string{"", "Work", "1work", "-work", "wo rk", "work_", strings.Repeat("a", 33)}
	for _, a := range good {
		if !ValidAlias(a) {
			t.Errorf("ValidAlias(%q) = false, want true", a)
		}
	}
	for _, a := range bad {
		if ValidAlias(a) {
			t.Errorf("ValidAlias(%q) = true, want false", a)
		}
	}
}

func TestLoadMissingFileGivesEmpty(t *testing.T) {
	f, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || len(f.Accounts) != 0 || f.Default != "" {
		t.Fatalf("unexpected empty file: %+v", f)
	}
}

func TestSaveLoadRoundTripAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	f := &File{Version: 1}
	if err := f.Add(acct("work", "control@example.com")); err != nil {
		t.Fatal(err)
	}
	if err := f.Add(acct("personal", "someone@gmail.com")); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, f); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700", st.Mode().Perm())
	}
	st, err = os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Default != "work" || len(got.Accounts) != 2 || got.Accounts[1].Email != "someone@gmail.com" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if !got.Accounts[0].AddedAt.Equal(f.Accounts[0].AddedAt) {
		t.Fatalf("added_at lost: %v", got.Accounts[0].AddedAt)
	}
}

func TestLoadCorruptFileNamesPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	os.WriteFile(path, []byte("{not json"), 0o600)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want error naming %s", err, path)
	}
}

func TestAddRejectsDuplicatesAndBadAlias(t *testing.T) {
	f := &File{Version: 1}
	if err := f.Add(acct("work", "control@example.com")); err != nil {
		t.Fatal(err)
	}
	if err := f.Add(acct("work", "other@example.com")); err == nil {
		t.Fatal("duplicate alias accepted")
	}
	if err := f.Add(acct("again", "Control@Example.com")); err == nil {
		t.Fatal("duplicate email (case-insensitive) accepted")
	}
	if err := f.Add(acct("Bad Alias", "x@example.com")); err == nil {
		t.Fatal("invalid alias accepted")
	}
	if len(f.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(f.Accounts))
	}
}

func TestFirstAccountBecomesDefault(t *testing.T) {
	f := &File{Version: 1}
	f.Add(acct("personal", "p@gmail.com"))
	f.Add(acct("work", "control@example.com"))
	if f.Default != "personal" {
		t.Fatalf("default = %q, want personal", f.Default)
	}
	if err := f.SetDefault("work"); err != nil {
		t.Fatal(err)
	}
	if f.Default != "work" {
		t.Fatalf("default = %q, want work", f.Default)
	}
	if err := f.SetDefault("nope"); err == nil {
		t.Fatal("unknown alias accepted as default")
	}
}

func TestReplaceUpdatesExistingAlias(t *testing.T) {
	f := &File{Version: 1}
	f.Add(acct("work", "old@example.com"))
	f.Add(acct("personal", "p@gmail.com"))
	if err := f.Replace(acct("work", "control@example.com")); err != nil {
		t.Fatal(err)
	}
	if a, _ := f.Find("work"); a.Email != "control@example.com" {
		t.Fatalf("email = %q", a.Email)
	}
	if err := f.Replace(acct("work", "p@gmail.com")); err == nil {
		t.Fatal("email owned by another alias accepted")
	}
	if err := f.Replace(acct("ghost", "g@example.com")); err == nil {
		t.Fatal("replace of unknown alias accepted")
	}
}

func TestRemoveClearsDefault(t *testing.T) {
	f := &File{Version: 1}
	f.Add(acct("work", "control@example.com"))
	f.Add(acct("personal", "p@gmail.com"))
	if err := f.Remove("work"); err != nil {
		t.Fatal(err)
	}
	if f.Default != "" || len(f.Accounts) != 1 || f.Accounts[0].Alias != "personal" {
		t.Fatalf("after remove: %+v", f)
	}
	if err := f.Remove("work"); err == nil {
		t.Fatal("removing unknown alias must error")
	}
}

func TestResolve(t *testing.T) {
	f := &File{Version: 1}
	f.Add(acct("work", "control@example.com"))
	f.Add(acct("personal", "p@gmail.com"))
	a, err := f.Resolve("")
	if err != nil || a.Alias != "work" {
		t.Fatalf("Resolve(\"\") = %+v, %v", a, err)
	}
	a, err = f.Resolve("personal")
	if err != nil || a.Email != "p@gmail.com" {
		t.Fatalf("Resolve(personal) = %+v, %v", a, err)
	}
	_, err = f.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "personal") || !strings.Contains(err.Error(), "work") {
		t.Fatalf("Resolve(nope) err = %v, want list of aliases", err)
	}
	f.Default = ""
	_, err = f.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "no default account") {
		t.Fatalf("Resolve(\"\") without default err = %v", err)
	}
	empty := &File{Version: 1}
	_, err = empty.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "no accounts") {
		t.Fatalf("empty Resolve err = %v", err)
	}
}

func TestValidDescription(t *testing.T) {
	if err := ValidDescription(""); err != nil {
		t.Fatalf("empty description rejected: %v", err)
	}
	if err := ValidDescription(strings.Repeat("a", 200)); err != nil {
		t.Fatalf("200 characters rejected: %v", err)
	}
	err := ValidDescription(strings.Repeat("a", 201))
	if err == nil || err.Error() != "description is too long (201 characters, maximum 200)" {
		t.Fatalf("err = %v", err)
	}
}

func TestDirEnvOverride(t *testing.T) {
	t.Setenv(EnvDir, "/tmp/mg-test-dir")
	dir, err := Dir()
	if err != nil || dir != "/tmp/mg-test-dir" {
		t.Fatalf("Dir() = %q, %v", dir, err)
	}
	t.Setenv(EnvDir, "")
	dir, err = Dir()
	if err != nil || !strings.HasSuffix(dir, filepath.Join(".config", "minuar-multi-gmail")) {
		t.Fatalf("Dir() = %q, %v", dir, err)
	}
}

func TestDescriptionRoundTripAndSetDescription(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	f := &File{Version: 1}
	work := acct("work", "you@company.example")
	work.Description = "the company mailbox; use it by default"
	if err := f.Add(work); err != nil {
		t.Fatal(err)
	}
	if err := f.Add(acct("personal", "you@gmail.com")); err != nil {
		t.Fatal(err)
	}
	if err := f.SetDescription("personal", "the user's personal mailbox"); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := got.Find("work")
	b, _ := got.Find("personal")
	if a.Description != "the company mailbox; use it by default" || b.Description != "the user's personal mailbox" {
		t.Fatalf("descriptions lost: %+v %+v", a, b)
	}
	if err := got.SetDescription("personal", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := got.Find("personal"); b.Description != "" {
		t.Fatalf("description not cleared: %+v", b)
	}
	if err := Save(dir, got); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"description"`) != 1 {
		t.Fatalf("an empty description must be omitted: %s", data)
	}
	// A file written before the field existed still loads.
	old := filepath.Join(t.TempDir(), "old")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(old, FileName), []byte(`{"version":1,"default":"work","accounts":[{"alias":"work","email":"you@company.example"}]}`), 0o600)
	oldFile, err := Load(old)
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := oldFile.Find("work"); !ok || w.Description != "" {
		t.Fatalf("old file = %+v", oldFile)
	}
	if err := oldFile.SetDescription("ghost", "x"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("SetDescription on unknown alias err = %v", err)
	}
}

func TestSecretsFieldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f := &File{Version: 1, Secrets: "file"}
	if err := Save(dir, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || got.Secrets != "file" {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	// An older file without the field loads with an empty kind.
	os.WriteFile(filepath.Join(dir, FileName), []byte(`{"version":1,"accounts":[]}`), 0o600)
	got, err = Load(dir)
	if err != nil || got.Secrets != "" {
		t.Fatalf("Load without the field = %+v, %v", got, err)
	}
}
