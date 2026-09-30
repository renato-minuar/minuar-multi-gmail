package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	fs := NewFile(dir)
	if _, err := fs.Get("oauth-client-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on an empty store = %v, want ErrNotFound", err)
	}
	if err := fs.Set("oauth-client-id", "id.apps"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("refresh-token.work", "1//0gRefresh"); err != nil {
		t.Fatal(err)
	}
	got, err := fs.Get("oauth-client-id")
	if err != nil || got != "id.apps" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	// A second store on the same directory reads what the first wrote.
	got, err = NewFile(dir).Get("refresh-token.work")
	if err != nil || got != "1//0gRefresh" {
		t.Fatalf("Get from a fresh store = %q, %v", got, err)
	}
	if err := fs.Delete("oauth-client-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Get("oauth-client-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := fs.Delete("oauth-client-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}
	got, err = fs.Get("refresh-token.work")
	if err != nil || got != "1//0gRefresh" {
		t.Fatalf("the other item must survive a Delete: %q, %v", got, err)
	}
}

func TestFileStoreModesAndAtomicWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are Unix only")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	fs := NewFile(dir)
	if err := fs.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	// A file that lost its mode (copied by hand) gets it back on the next write.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("b", "2"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600 (err %v)", st.Mode().Perm(), err)
	}
	dst, err := os.Stat(dir)
	if err != nil || dst.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700 (err %v)", dst.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != FileName {
		t.Fatalf("dir holds %v, want only %s", entries, FileName)
	}
}

func TestFileStoreRejectsInvalidSecretBeforeWriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	fs := NewFile(dir)
	if err := fs.Set("a", "has space"); err == nil {
		t.Fatal("invalid secret accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		t.Fatal("nothing may be written for a rejected secret")
	}
}

func TestFileStoreReturnsForeignValuesAsIs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte(`{"a":"has space"}`), 0o600)
	got, err := NewFile(dir).Get("a")
	if err != nil || got != "has space" {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

func TestFileStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o600)
	fs := NewFile(dir)
	if _, err := fs.Get("a"); err == nil || !strings.Contains(err.Error(), FileName) {
		t.Fatalf("Get on a corrupt file = %v, want an error naming the file", err)
	}
	if err := fs.Set("a", "1"); err == nil {
		t.Fatal("Set must not overwrite a corrupt file")
	}
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if string(data) != "{not json" {
		t.Fatalf("corrupt file was changed to %q", data)
	}
}
