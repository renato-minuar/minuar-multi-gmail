package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

func TestOpenStoreEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	if err := config.Save(dir, &config.File{Version: 1, Secrets: "keychain"}); err != nil {
		t.Fatal(err)
	}
	kind, st, err := openStoreOn(dir, "linux", "file")
	if err != nil || kind != secrets.KindFile || st == nil {
		t.Fatalf("kind %q store %v err %v", kind, st, err)
	}
	if _, _, err := openStoreOn(dir, "linux", "vault"); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("unknown env kind: %v", err)
	}
}

func TestOpenStoreReadsTheRecordedKind(t *testing.T) {
	dir := t.TempDir()
	if err := config.Save(dir, &config.File{Version: 1, Secrets: "file"}); err != nil {
		t.Fatal(err)
	}
	kind, st, err := openStoreOn(dir, "linux", "")
	if err != nil || kind != secrets.KindFile {
		t.Fatalf("kind %q err %v", kind, err)
	}
	if err := st.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := secrets.NewFile(dir).Get("a"); got != "1" {
		t.Fatalf("the store must be the file in the config dir, Get = %q", got)
	}
	// A recorded kind the system cannot serve is refused with both names.
	if _, _, err := openStoreOn(dir, "darwin", "secret-service"); err == nil || !strings.Contains(err.Error(), "darwin") {
		t.Fatalf("secret-service on darwin: %v", err)
	}
}

func TestOpenStoreEmptyKind(t *testing.T) {
	dir := t.TempDir()
	// No accounts.json at all: an install from before the field existed.
	kind, _, err := openStoreOn(dir, "darwin", "")
	if err != nil || kind != secrets.KindKeychain {
		t.Fatalf("darwin without a recorded kind: %q, %v", kind, err)
	}
	_, _, err = openStoreOn(dir, "linux", "")
	if err == nil || !strings.Contains(err.Error(), "minuar-multi-gmail setup") {
		t.Fatalf("linux without a recorded kind: %v, want a pointer to setup", err)
	}
}

func TestOpenStoreCorruptConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, config.FileName), []byte("{not json"), 0o600)
	if _, _, err := openStoreOn(dir, "darwin", ""); err == nil {
		t.Fatal("corrupt config must not fall back to the Keychain silently")
	}
}

func TestFailingStore(t *testing.T) {
	boom := errors.New("no secret store recorded")
	var st secrets.Store = failingStore{err: boom}
	if _, err := st.Get("a"); !errors.Is(err, boom) {
		t.Fatalf("Get = %v", err)
	}
	if err := st.Set("a", "1"); !errors.Is(err, boom) {
		t.Fatalf("Set = %v", err)
	}
	if err := st.Delete("a"); !errors.Is(err, boom) {
		t.Fatalf("Delete = %v", err)
	}
}
