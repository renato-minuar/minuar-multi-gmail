# Secret Store Per System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The binary picks the best secret store the system offers at `setup`, records the choice, and runs on macOS, Linux desktops and Linux servers with no new dependency.

**Architecture:** `internal/secrets` gains two `Store` implementations (`secret-tool` shell-out, JSON file) plus a `Kind` type with `Detect` and `Open`. `config.File` records the chosen `Kind`. Every command opens its store through one `openStore` in `cmd`, and `setup` is the only place a choice is made. The browser opener picks its command per OS and a failed open no longer aborts the login.

**Tech Stack:** Go 1.26 standard library only. Existing test style: table-free `t.Fatalf` tests, fake runners, `t.TempDir()`.

**Spec:** `docs/superpowers/specs/2026-09-30-secret-store-per-system-design.md`

## Global Constraints

- No new module in `go.mod`. `go.sum` stays as it is.
- `ValidSecret` (charset `^[A-Za-z0-9._~/+=-]+$`) applies to every store's `Set`.
- File mode 0600 for secret files, 0700 for their directory, atomic write (temp file in the same directory, then rename), as `config.Save` does.
- The macOS `keychainStore` code and its tests stay unchanged.
- Commit messages: imperative subject with a conventional prefix, body says what changed and why. No co-author lines, no tool trailers, no model names.
- `scripts/verify.sh` (gofmt, vet, build, `go test -race ./...`) passes after every task.
- Comments and help texts say "secret store" for the store in general; "Keychain" only for the macOS store.

## Review Focus

1. `secrets.json` written by a previous run with mode 0644 (copied by hand, restored from a backup): `fileStore.save` must chmod the new file to 0600 on every write, and the test for the file mode covers the second write, not only the first.
2. A `secrets.json` that holds a value with characters outside `ValidSecret`: `Get` returns it as is (the file is the user's), `Set` of such a value is refused. Test in Task 1.
3. `secret-tool lookup` exit 1 with a non-empty stderr (D-Bus not reachable) must not read as "not found": it is a failure with the stderr text. Test in Task 2.
4. `MINUAR_MULTI_GMAIL_SECRETS=keychain` on Linux, or `secret-service` on macOS: `Open` refuses with a message that names the kind and the system, so a wrong environment cannot make `setup` write into a store no later command can read. Test in Task 3.
5. `accounts.json` with `"secrets": "file"` but no `secrets.json` yet (setup interrupted after writing the config): every command reports `secret not found` for the OAuth client, the same message as a missing Keychain item, and `doctor` says which store it looked in. Covered by the `doctor` test in Task 5.

---

### Task 1: File store

**Files:**
- Create: `internal/secrets/file.go`
- Create: `internal/secrets/file_test.go`

**Interfaces:**
- Consumes: `Store`, `ErrNotFound`, `ValidSecret` from `internal/secrets/secrets.go`.
- Produces: `func NewFile(dir string) Store` and `const FileName = "secrets.json"`.

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /Users/benne-air/projects/minuar-multi-gmail && go test ./internal/secrets/ -run FileStore`
Expected: build failure, `undefined: NewFile` and `undefined: FileName`.

- [ ] **Step 3: Write the implementation**

```go
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileName is the secrets file inside the config directory.
const FileName = "secrets.json"

// fileStore keeps secrets in a JSON object on disk. It is the store for
// systems without a keyring: anyone with the user's account, or root,
// can read it, which setup states before the user accepts it.
type fileStore struct {
	mu   sync.Mutex
	dir  string
	path string
}

// NewFile returns a Store backed by dir/secrets.json.
func NewFile(dir string) Store {
	return &fileStore{dir: dir, path: filepath.Join(dir, FileName)}
}

// load reads the file. A missing file is an empty store.
func (f *fileStore) load() (map[string]string, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.path, err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w (fix or move the file; it is never overwritten automatically)", f.path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// save writes atomically: temp file in the same directory, mode 0600,
// then rename. The directory mode is set on every write so a directory
// created by hand ends up private too.
func (f *fileStore) save(m map[string]string) error {
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", f.dir, err)
	}
	if err := os.Chmod(f.dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", f.dir, err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(f.dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", f.dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpPath) }
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, f.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	return nil
}

func (f *fileStore) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileStore) Set(name, secret string) error {
	if !ValidSecret(secret) {
		return fmt.Errorf("secret for %s contains characters outside the allowed set", name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[name] = secret
	return f.save(m)
}

func (f *fileStore) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return ErrNotFound
	}
	delete(m, name)
	return f.save(m)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/secrets/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/file.go internal/secrets/file_test.go
git commit -m "feat(secrets): add the file store for systems without a keyring" -m "secrets.json in the config directory, mode 0600, written atomically like accounts.json. It is the store for Linux servers and Windows, where no keyring answers; setup makes the user accept it before use."
```

---

### Task 2: secret-tool store

**Files:**
- Create: `internal/secrets/secrettool.go`
- Create: `internal/secrets/secrettool_test.go`

**Interfaces:**
- Consumes: `runResult`, `runSecurity`'s shape from `internal/secrets/keychain.go` (the `run func(args []string, stdin string) runResult` field), `fakeRunner` from `keychain_test.go`.
- Produces: `func NewSecretTool(service string) Store`, unexported `newSecretToolStore(service string, run func([]string, string) runResult) *secretToolStore`.

- [ ] **Step 1: Write the failing tests**

```go
package secrets

import (
	"errors"
	"strings"
	"testing"
)

func newFakeSecretTool(results ...runResult) (*secretToolStore, *fakeRunner) {
	fr := &fakeRunner{results: results}
	return newSecretToolStore("svc.test", fr.run), fr
}

func TestSecretToolSetUsesStdinAndReadsBack(t *testing.T) {
	st, fr := newFakeSecretTool(runResult{exit: 0}, runResult{exit: 0, stdout: "tok\n"})
	if err := st.Set("refresh-token.work", "tok"); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls = %d, want store then lookup", len(fr.calls))
	}
	store := fr.calls[0]
	if strings.Join(store.args, " ") != "store --label=minuar-multi-gmail refresh-token.work service svc.test account refresh-token.work" {
		t.Fatalf("store args = %q", store.args)
	}
	if store.stdin != "tok\n" {
		t.Fatalf("secret must travel on stdin with a newline, got %q", store.stdin)
	}
	if strings.Join(fr.calls[1].args, " ") != "lookup service svc.test account refresh-token.work" {
		t.Fatalf("lookup args = %q", fr.calls[1].args)
	}
}

func TestSecretToolSetFailsWhenReadBackDiffers(t *testing.T) {
	st, _ := newFakeSecretTool(runResult{exit: 0}, runResult{exit: 0, stdout: "other\n"})
	if err := st.Set("a", "tok"); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretToolSetRejectsInvalidSecretBeforeRunning(t *testing.T) {
	st, fr := newFakeSecretTool()
	if err := st.Set("a", "has space"); err == nil {
		t.Fatal("invalid secret accepted")
	}
	if len(fr.calls) != 0 {
		t.Fatalf("secret-tool ran %d times for an invalid secret", len(fr.calls))
	}
}

func TestSecretToolSetPropagatesFailure(t *testing.T) {
	st, _ := newFakeSecretTool(runResult{exit: 1, stderr: "secret-tool: Cannot autolaunch D-Bus without X11 $DISPLAY"})
	err := st.Set("a", "tok")
	if err == nil || !strings.Contains(err.Error(), "Cannot autolaunch D-Bus") {
		t.Fatalf("err = %v, want the stderr text", err)
	}
}

func TestSecretToolGetMissingIsErrNotFound(t *testing.T) {
	st, _ := newFakeSecretTool(runResult{exit: 1})
	_, err := st.Get("a")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Exit 1 with text on stderr is a broken keyring, not a missing item.
func TestSecretToolGetExitOneWithStderrIsAFailure(t *testing.T) {
	st, _ := newFakeSecretTool(runResult{exit: 1, stderr: "secret-tool: The name org.freedesktop.secrets was not provided by any .service files"})
	_, err := st.Get("a")
	if errors.Is(err, ErrNotFound) || err == nil || !strings.Contains(err.Error(), "org.freedesktop.secrets") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretToolGetTrimsTrailingNewline(t *testing.T) {
	st, _ := newFakeSecretTool(runResult{exit: 0, stdout: "tok\n"})
	got, err := st.Get("a")
	if err != nil || got != "tok" {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

func TestSecretToolGetProcessStartFailure(t *testing.T) {
	st, _ := newFakeSecretTool(runResult{err: errors.New("exec: \"secret-tool\": executable file not found in $PATH")})
	_, err := st.Get("a")
	if err == nil || !strings.Contains(err.Error(), "not found in $PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretToolDelete(t *testing.T) {
	st, fr := newFakeSecretTool(runResult{exit: 0, stdout: "tok\n"}, runResult{exit: 0})
	if err := st.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 2 || strings.Join(fr.calls[1].args, " ") != "clear service svc.test account a" {
		t.Fatalf("calls = %+v", fr.calls)
	}
	st, fr = newFakeSecretTool(runResult{exit: 1})
	if err := st.Delete("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete of a missing item = %v, want ErrNotFound", err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("clear must not run for a missing item: %+v", fr.calls)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/secrets/ -run SecretTool`
Expected: build failure, `undefined: newSecretToolStore`.

- [ ] **Step 3: Write the implementation**

```go
package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// secretToolStore is the Linux desktop store. It shells out to
// secret-tool, the libsecret command that talks to the Secret Service
// (GNOME Keyring, KWallet) over the session bus, the same shape as the
// macOS keychainStore. Items carry two attributes, service and account,
// so lookups are exact.
type secretToolStore struct {
	service string
	run     func(args []string, stdin string) runResult
}

// NewSecretTool returns a Store backed by the Secret Service under service.
func NewSecretTool(service string) Store {
	return newSecretToolStore(service, runSecretTool)
}

func newSecretToolStore(service string, run func([]string, string) runResult) *secretToolStore {
	return &secretToolStore{service: service, run: run}
}

func runSecretTool(args []string, stdin string) runResult {
	cmd := exec.Command("secret-tool", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	res := runResult{stdout: out.String(), stderr: errb.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.exit = exitErr.ExitCode()
	default:
		res.err = err
	}
	return res
}

func (s *secretToolStore) fail(op string, r runResult) error {
	if r.err != nil {
		return fmt.Errorf("secret-tool %s: %w", op, r.err)
	}
	return fmt.Errorf("secret-tool %s failed (exit %d): %s", op, r.exit, strings.TrimSpace(r.stderr))
}

func (s *secretToolStore) attrs(name string) []string {
	return []string{"service", s.service, "account", name}
}

// Get runs secret-tool lookup. Exit 1 with nothing on stderr is "no such
// item"; exit 1 with a message is a keyring that cannot be reached.
func (s *secretToolStore) Get(name string) (string, error) {
	r := s.run(append([]string{"lookup"}, s.attrs(name)...), "")
	switch {
	case r.err != nil:
		return "", s.fail("lookup", r)
	case r.exit == 0:
		return strings.TrimRight(r.stdout, "\r\n"), nil
	case r.exit == 1 && strings.TrimSpace(r.stderr) == "":
		return "", ErrNotFound
	default:
		return "", s.fail("lookup", r)
	}
}

// Set writes through secret-tool store with the secret on stdin, then
// reads the item back and compares, as the Keychain store does.
func (s *secretToolStore) Set(name, secret string) error {
	if !ValidSecret(secret) {
		return fmt.Errorf("secret for %s contains characters outside the allowed set", name)
	}
	args := append([]string{"store", "--label=minuar-multi-gmail " + name}, s.attrs(name)...)
	r := s.run(args, secret+"\n")
	if r.err != nil || r.exit != 0 {
		return s.fail("store", r)
	}
	got, err := s.Get(name)
	if err != nil {
		return fmt.Errorf("read back %s after write: %w", name, err)
	}
	if got != secret {
		return fmt.Errorf("read back %s after write: stored value differs", name)
	}
	return nil
}

// Delete looks the item up first, because secret-tool clear exits 0
// whether or not anything matched.
func (s *secretToolStore) Delete(name string) error {
	if _, err := s.Get(name); err != nil {
		return err
	}
	r := s.run(append([]string{"clear"}, s.attrs(name)...), "")
	if r.err != nil || r.exit != 0 {
		return s.fail("clear", r)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/secrets/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/secrettool.go internal/secrets/secrettool_test.go
git commit -m "feat(secrets): add the secret-tool store for Linux desktops" -m "Shells out to secret-tool (libsecret) the way the macOS store shells out to security: secret on stdin, read back after every write, exit 1 with an empty stderr is the only 'not found'."
```

---

### Task 3: Store kind, detection, opening, and the config field

**Files:**
- Create: `internal/secrets/kind.go`
- Create: `internal/secrets/kind_test.go`
- Modify: `internal/config/config.go:38-42` (the `File` struct)
- Modify: `internal/config/config_test.go` (add one test)

**Interfaces:**
- Consumes: `NewKeychain`, `NewSecretTool`, `NewFile`, `ServiceName` from `internal/secrets`.
- Produces:
  - `type Kind string`; `const KindKeychain Kind = "keychain"`, `KindSecretService Kind = "secret-service"`, `KindFile Kind = "file"`.
  - `const EnvKind = "MINUAR_MULTI_GMAIL_SECRETS"`.
  - `func ParseKind(s string) (Kind, error)`.
  - `func Detect(service string) (Kind, string)` and the testable `detect(service string, d detectDeps) (Kind, string)`.
  - `func Open(kind Kind, service, configDir string) (Store, error)` and `func OpenOn(kind Kind, service, configDir, goos string) (Store, error)`.
  - `config.File.Secrets secrets.Kind` — no: `config` must not import `secrets` (secrets imports nothing from config today, but keep the packages independent). The field is `Secrets string \`json:"secrets,omitempty"\``; callers convert with `secrets.Kind(cfg.Secrets)`.

- [ ] **Step 1: Write the failing tests for `secrets`**

```go
package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestParseKind(t *testing.T) {
	for _, s := range []string{"keychain", "secret-service", "file"} {
		k, err := ParseKind(s)
		if err != nil || string(k) != s {
			t.Fatalf("ParseKind(%q) = %q, %v", s, k, err)
		}
	}
	if _, err := ParseKind("vault"); err == nil || !strings.Contains(err.Error(), "keychain, secret-service, file") {
		t.Fatalf("ParseKind(vault) = %v, want the list of kinds", err)
	}
	if _, err := ParseKind(""); err == nil {
		t.Fatal("empty kind accepted")
	}
}

func fixedDeps(goos string, env map[string]string, tools map[string]bool, probeErr error) detectDeps {
	return detectDeps{
		goos:   goos,
		getenv: func(k string) string { return env[k] },
		lookPath: func(name string) (string, error) {
			if tools[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		probe: func(string) error { return probeErr },
	}
}

func TestDetectEnvironmentWins(t *testing.T) {
	d := fixedDeps("darwin", map[string]string{EnvKind: "file"}, nil, nil)
	k, why := detect("svc", d)
	if k != KindFile || !strings.Contains(why, EnvKind) {
		t.Fatalf("detect = %q, %q", k, why)
	}
	d = fixedDeps("linux", map[string]string{EnvKind: "vault"}, nil, nil)
	k, why = detect("svc", d)
	if k != "" || !strings.Contains(why, "vault") {
		t.Fatalf("unknown env kind: detect = %q, %q", k, why)
	}
}

func TestDetectPerSystem(t *testing.T) {
	k, why := detect("svc", fixedDeps("darwin", nil, nil, nil))
	if k != KindKeychain || !strings.Contains(why, "Keychain") {
		t.Fatalf("darwin: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("windows", nil, nil, nil))
	if k != KindFile || !strings.Contains(why, "secrets.json") {
		t.Fatalf("windows: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("linux", nil, map[string]bool{"secret-tool": true}, nil))
	if k != KindSecretService || !strings.Contains(why, "Secret Service") {
		t.Fatalf("linux with a keyring: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("linux", nil, map[string]bool{"secret-tool": true}, errors.New("Cannot autolaunch D-Bus")))
	if k != KindFile || !strings.Contains(why, "Cannot autolaunch D-Bus") || !strings.Contains(why, "libsecret-tools") {
		t.Fatalf("linux with a dead keyring: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("linux", nil, nil, nil))
	if k != KindFile || !strings.Contains(why, "secret-tool") || !strings.Contains(why, "libsecret-tools") {
		t.Fatalf("linux without secret-tool: %q, %q", k, why)
	}
	k, why = detect("svc", fixedDeps("freebsd", nil, nil, nil))
	if k != KindFile {
		t.Fatalf("other unix: %q, %q", k, why)
	}
}

func TestOpenOn(t *testing.T) {
	dir := "/tmp/cfg"
	if s, err := OpenOn(KindKeychain, "svc", dir, "darwin"); err != nil || s == nil {
		t.Fatalf("keychain on darwin: %v", err)
	}
	if _, err := OpenOn(KindKeychain, "svc", dir, "linux"); err == nil || !strings.Contains(err.Error(), "keychain") || !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("keychain on linux = %v", err)
	}
	if s, err := OpenOn(KindSecretService, "svc", dir, "linux"); err != nil || s == nil {
		t.Fatalf("secret-service on linux: %v", err)
	}
	if _, err := OpenOn(KindSecretService, "svc", dir, "windows"); err == nil || !strings.Contains(err.Error(), "secret-service") {
		t.Fatalf("secret-service on windows = %v", err)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if s, err := OpenOn(KindFile, "svc", dir, goos); err != nil || s == nil {
			t.Fatalf("file on %s: %v", goos, err)
		}
	}
	if _, err := OpenOn("vault", "svc", dir, "linux"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestDescribeKind(t *testing.T) {
	if got := KindKeychain.Describe("/tmp/cfg"); got != "macOS Keychain" {
		t.Fatalf("keychain = %q", got)
	}
	if got := KindSecretService.Describe("/tmp/cfg"); got != "Secret Service keyring (secret-tool)" {
		t.Fatalf("secret-service = %q", got)
	}
	if got := KindFile.Describe("/tmp/cfg"); got != "file /tmp/cfg/secrets.json" {
		t.Fatalf("file = %q", got)
	}
}
```

- [ ] **Step 2: Write the failing test for `config`**

Append to `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/secrets/ ./internal/config/`
Expected: build failures, `undefined: ParseKind`, `undefined: detectDeps`, `unknown field Secrets`.

- [ ] **Step 4: Add the config field**

In `internal/config/config.go`, change the `File` struct to:

```go
type File struct {
	Version int    `json:"version"`
	Default string `json:"default,omitempty"`
	// Secrets is the secret store kind chosen by setup: "keychain",
	// "secret-service" or "file". Empty in files written before the
	// choice existed, which every command reads as the macOS Keychain.
	Secrets  string    `json:"secrets,omitempty"`
	Accounts []Account `json:"accounts"`
}
```

Also change the package comment's second sentence to: "It never holds secrets; it records which store does."

- [ ] **Step 5: Write `internal/secrets/kind.go`**

```go
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Kind names a secret store. setup picks one, records it in accounts.json,
// and every later command opens that one.
type Kind string

const (
	KindKeychain      Kind = "keychain"       // macOS: security command
	KindSecretService Kind = "secret-service" // Linux desktop: secret-tool
	KindFile          Kind = "file"           // any system: secrets.json

	// EnvKind forces a kind. It wins over the recorded choice everywhere.
	EnvKind = "MINUAR_MULTI_GMAIL_SECRETS"
)

var kinds = []Kind{KindKeychain, KindSecretService, KindFile}

func kindList() string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

func ParseKind(s string) (Kind, error) {
	for _, k := range kinds {
		if Kind(s) == k {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown secret store %q (one of: %s)", s, kindList())
}

// Describe says where secrets of this kind live, for setup and doctor.
func (k Kind) Describe(configDir string) string {
	switch k {
	case KindKeychain:
		return "macOS Keychain"
	case KindSecretService:
		return "Secret Service keyring (secret-tool)"
	case KindFile:
		return "file " + filepath.Join(configDir, FileName)
	}
	return string(k)
}

// detectDeps is what Detect reads from the system, injectable in tests.
type detectDeps struct {
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	// probe does a store, lookup, clear round-trip through secret-tool
	// and returns the first error.
	probe func(service string) error
}

func defaultDetectDeps() detectDeps {
	return detectDeps{goos: runtime.GOOS, getenv: os.Getenv, lookPath: exec.LookPath, probe: probeSecretTool}
}

// probeSecretTool writes, reads and clears a throwaway item so the choice
// rests on a working keyring, not on the command being installed.
func probeSecretTool(service string) error {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	name := "probe-" + hex.EncodeToString(b)
	st := NewSecretTool(service)
	if err := st.Set(name, "probe"); err != nil {
		return err
	}
	return st.Delete(name)
}

const installHint = "Install secret-tool (package libsecret-tools on Debian and Ubuntu, libsecret on Fedora and Arch) and log in to a desktop session for a keyring."

// Detect returns the kind this system supports and one sentence for the
// user. An unknown value in the environment returns an empty kind and
// the error text as the sentence.
func Detect(service string) (Kind, string) { return detect(service, defaultDetectDeps()) }

func detect(service string, d detectDeps) (Kind, string) {
	if v := d.getenv(EnvKind); v != "" {
		k, err := ParseKind(v)
		if err != nil {
			return "", err.Error()
		}
		return k, fmt.Sprintf("Secret store forced to %s by %s.", k, EnvKind)
	}
	switch d.goos {
	case "darwin":
		return KindKeychain, "Secrets go to the macOS Keychain."
	case "windows":
		return KindFile, "This version has no keyring store for Windows; secrets go to secrets.json in the config directory, readable by your Windows user."
	}
	if _, err := d.lookPath("secret-tool"); err != nil {
		return KindFile, "No keyring found: secret-tool is not installed. " + installHint
	}
	if err := d.probe(service); err != nil {
		return KindFile, fmt.Sprintf("No keyring answered: %v. %s", err, installHint)
	}
	return KindSecretService, "Secrets go to the Secret Service keyring of your desktop session (secret-tool)."
}

// Open builds the store for kind on this system.
func Open(kind Kind, service, configDir string) (Store, error) {
	return OpenOn(kind, service, configDir, runtime.GOOS)
}

// OpenOn is Open with the operating system as a parameter. A kind the
// system cannot serve is refused here so a wrong environment variable
// cannot make setup write where no later command can read.
func OpenOn(kind Kind, service, configDir, goos string) (Store, error) {
	switch kind {
	case KindKeychain:
		if goos != "darwin" {
			return nil, fmt.Errorf("secret store keychain exists only on macOS (this is %s)", goos)
		}
		return NewKeychain(service), nil
	case KindSecretService:
		if goos == "windows" || goos == "darwin" {
			return nil, fmt.Errorf("secret store secret-service exists only on Linux and other Unix systems (this is %s)", goos)
		}
		return NewSecretTool(service), nil
	case KindFile:
		return NewFile(configDir), nil
	}
	return nil, fmt.Errorf("unknown secret store %q (one of: %s)", kind, kindList())
}
```

Also change the second line of the package comment in `internal/secrets/secrets.go` from "The only production store is the macOS Keychain." to "Production stores: the macOS Keychain, the Linux Secret Service through secret-tool, and a file for systems without a keyring. setup picks one per system (kind.go)."

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/secrets/ ./internal/config/`
Expected: `ok` for both.

- [ ] **Step 7: Commit**

```bash
git add internal/secrets/kind.go internal/secrets/kind_test.go internal/secrets/secrets.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(secrets): detect and open the store per system, record the kind" -m "Detect picks keychain on macOS, secret-service on a Linux desktop whose keyring passes a round-trip, and file elsewhere or when MINUAR_MULTI_GMAIL_SECRETS forces it. OpenOn refuses a kind the system cannot serve. accounts.json gains a secrets field so later commands open the store setup chose instead of probing again."
```

---

### Task 4: Browser open per system, login survives a failed open

**Files:**
- Create: `cmd/minuar-multi-gmail/browser.go`
- Create: `cmd/minuar-multi-gmail/browser_test.go`
- Modify: `cmd/minuar-multi-gmail/addaccount.go:56` (delete `openBrowser`) and its `os/exec` import
- Modify: `internal/googleauth/login.go:101-105`
- Modify: `internal/googleauth/login_test.go` (add one test)

**Interfaces:**
- Consumes: `googleauth.LoginOptions.OpenURL`, `LoginOptions.Notify`.
- Produces: `func openBrowser(url string) error` (same name as before, used by `addaccount.go:121`), `func browserCommand(goos, url string) *exec.Cmd`.

- [ ] **Step 1: Write the failing tests**

`cmd/minuar-multi-gmail/browser_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestBrowserCommandPerSystem(t *testing.T) {
	cases := map[string]string{
		"darwin":  "open https://x.example/a",
		"windows": "rundll32 url.dll,FileProtocolHandler https://x.example/a",
		"linux":   "xdg-open https://x.example/a",
		"freebsd": "xdg-open https://x.example/a",
	}
	for goos, want := range cases {
		cmd := browserCommand(goos, "https://x.example/a")
		if got := strings.Join(cmd.Args, " "); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
}
```

Append to `internal/googleauth/login_test.go`:

```go
// A machine without a browser (a server over SSH) still completes the
// login: the URL is printed, the open fails, the callback arrives.
func TestLoginContinuesWhenBrowserCannotOpen(t *testing.T) {
	g := newFakeGoogle(t, "rt")
	var saw url.Values
	open := browser(t, "", &saw)
	var notes strings.Builder
	tok, err := Login(context.Background(), LoginOptions{
		Creds: ClientCreds{ID: "id", Secret: "sec"},
		OpenURL: func(u string) error {
			open(u) // the test's stand-in for the user pasting the URL
			return errors.New("exec: \"xdg-open\": executable file not found in $PATH")
		},
		Endpoint: g.endpoint(), Timeout: 5 * time.Second, Notify: &notes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken != "rt" {
		t.Fatalf("token = %+v", tok)
	}
	if !strings.Contains(notes.String(), "Could not open a browser") || !strings.Contains(notes.String(), "xdg-open") {
		t.Fatalf("notify = %q", notes.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run BrowserCommand; go test ./internal/googleauth/ -run ContinuesWhenBrowser`
Expected: `undefined: browserCommand`; the login test fails with `login: open browser: exec: "xdg-open"...`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/browser.go` and remove the old opener**

```go
package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url with the system's own opener. add-account prints
// the URL before calling this, so a failure here is not fatal: the login
// keeps waiting for the callback.
func openBrowser(url string) error { return browserCommand(runtime.GOOS, url).Run() }

func browserCommand(goos, url string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return exec.Command("xdg-open", url)
	}
}
```

In `cmd/minuar-multi-gmail/addaccount.go` delete the line `func openBrowser(url string) error { return exec.Command("open", url).Run() }` and the `"os/exec"` import.

- [ ] **Step 4: Change `Login` to continue after a failed open**

In `internal/googleauth/login.go` replace

```go
	if err := o.OpenURL(authURL); err != nil {
		return nil, fmt.Errorf("login: open browser: %w", err)
	}
```

with

```go
	if err := o.OpenURL(authURL); err != nil && o.Notify != nil {
		// The URL is already on screen. A machine without a browser
		// (a server over SSH) completes the login by hand.
		fmt.Fprintf(o.Notify, "Could not open a browser (%v). Open the URL above yourself.\n", err)
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`.

- [ ] **Step 6: Commit**

```bash
git add cmd/minuar-multi-gmail/browser.go cmd/minuar-multi-gmail/browser_test.go cmd/minuar-multi-gmail/addaccount.go internal/googleauth/login.go internal/googleauth/login_test.go
git commit -m "feat(login): open the browser per system and keep waiting when it fails" -m "open on macOS, xdg-open on Linux, rundll32 on Windows. The login URL is printed before the open, so a failed open now prints a note and waits for the callback instead of aborting; that is the path for a login over an SSH port forward."
```

---

### Task 5: One place that opens the store; commands use it

**Files:**
- Create: `cmd/minuar-multi-gmail/store.go`
- Create: `cmd/minuar-multi-gmail/store_test.go`
- Modify: `cmd/minuar-multi-gmail/addaccount.go:112-120`
- Modify: `cmd/minuar-multi-gmail/accounts.go:88-97`
- Modify: `cmd/minuar-multi-gmail/doctor.go` (deps, help text, first report line)
- Modify: `cmd/minuar-multi-gmail/serve.go:99-108`
- Modify: `cmd/minuar-multi-gmail/commands_test.go:359-407` (`TestRunDoctor`)
- Modify: `cmd/minuar-multi-gmail/serve_integration_test.go:51-55, 162-172`

**Interfaces:**
- Consumes: `secrets.Kind`, `secrets.EnvKind`, `secrets.KindKeychain`, `secrets.OpenOn`, `secrets.ServiceName`, `config.Load`, `config.File.Secrets`.
- Produces: `func openStore(configDir string) (secrets.Kind, secrets.Store, error)`, `func openStoreOn(configDir, goos, envKind string) (secrets.Kind, secrets.Store, error)`, `type failingStore struct{ err error }` implementing `secrets.Store`, `doctorDeps.storeKind secrets.Kind`.

- [ ] **Step 1: Write the failing tests**

`cmd/minuar-multi-gmail/store_test.go`:

```go
package main

import (
	"errors"
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
```

Add `"os"` and `"path/filepath"` to that file's imports.

In `cmd/minuar-multi-gmail/commands_test.go`, inside `TestRunDoctor`, find the first `runDoctor(` call and add `storeKind: secrets.KindFile` to its `doctorDeps` literal; then after the first `out.String()` check add:

```go
	if !strings.HasPrefix(out.String(), "ok   secrets        file") {
		t.Fatalf("doctor must name the store first: %q", out.String())
	}
```

(Read the existing test first: the `doctorDeps` literal is built in that test; add the field to every literal in it. The exact prefix comes from `report`'s format `"%s %-14s %s\n"`.)

In `cmd/minuar-multi-gmail/serve_integration_test.go`:

- At line 54, extend the environment: `secrets.EnvKind+"=file"` after the attachment dir entry, so the test runs the same store on every system.
- After the unknown-account check (line 172), add:

```go
	// A known account with an empty file store: the tool call reaches the
	// store and reports the missing OAuth client instead of crashing.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "search_threads", Arguments: map[string]any{"query": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "secret not found") {
		t.Fatalf("search_threads with an empty secret store: %+v", res.Content)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/...`
Expected: build failure, `undefined: openStoreOn`, `undefined: failingStore`, `unknown field storeKind`.

- [ ] **Step 3: Write `cmd/minuar-multi-gmail/store.go`**

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/renato-minuar/minuar-multi-gmail/internal/config"
	"github.com/renato-minuar/minuar-multi-gmail/internal/secrets"
)

// openStore opens the secret store every command uses: the kind forced by
// MINUAR_MULTI_GMAIL_SECRETS, else the kind setup recorded in
// accounts.json. It never probes the system; only setup does that, so the
// store does not depend on how the process was started.
func openStore(configDir string) (secrets.Kind, secrets.Store, error) {
	return openStoreOn(configDir, runtime.GOOS, os.Getenv(secrets.EnvKind))
}

func openStoreOn(configDir, goos, envKind string) (secrets.Kind, secrets.Store, error) {
	var kind secrets.Kind
	if envKind != "" {
		k, err := secrets.ParseKind(envKind)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", secrets.EnvKind, err)
		}
		kind = k
	} else {
		cfg, err := config.Load(configDir)
		if err != nil {
			return "", nil, fmt.Errorf("load accounts: %w", err)
		}
		kind = secrets.Kind(cfg.Secrets)
	}
	if kind == "" {
		// Files written before the field existed: every such install is a
		// Mac using the Keychain.
		if goos != "darwin" {
			return "", nil, errors.New("no secret store recorded: run minuar-multi-gmail setup first")
		}
		kind = secrets.KindKeychain
	}
	st, err := secrets.OpenOn(kind, secrets.ServiceName(), configDir, goos)
	if err != nil {
		return "", nil, err
	}
	return kind, st, nil
}

// failingStore answers every call with one error. serve uses it when the
// store cannot be opened, so the server still starts and each tool call
// carries the message instead of Claude Code showing a dead server.
type failingStore struct{ err error }

func (f failingStore) Get(string) (string, error) { return "", f.err }
func (f failingStore) Set(string, string) error   { return f.err }
func (f failingStore) Delete(string) error        { return f.err }
```

- [ ] **Step 4: Use it in the commands**

`addaccount.go`, inside the `add-account` command after `dir, err := config.Dir()` handling, replace `store: secrets.NewKeychain(secrets.ServiceName()),` with `store: store,` and before the `d := addAccountDeps{` line add:

```go
		_, store, err := openStore(dir)
		if err != nil {
			fmt.Fprintln(io.err, "error:", err)
			return 1
		}
```

`accounts.go`, in the `remove-account` command, replace

```go
		if err == nil {
			revoke := func(ctx context.Context, tok string) error { return googleauth.Revoke(ctx, "", tok) }
			err = runRemoveAccount(ctx, secrets.NewKeychain(secrets.ServiceName()), dir, args[0], revoke, io.out)
		}
```

with

```go
		var store secrets.Store
		if err == nil {
			_, store, err = openStore(dir)
		}
		if err == nil {
			revoke := func(ctx context.Context, tok string) error { return googleauth.Revoke(ctx, "", tok) }
			err = runRemoveAccount(ctx, store, dir, args[0], revoke, io.out)
		}
```

`doctor.go`: add `storeKind secrets.Kind` to `doctorDeps`; change the help text to `"check the secret store, config and every account's token"`; in the command, replace `store: secrets.NewKeychain(secrets.ServiceName()), configDir: dir, out: io.out,` with `store: store, storeKind: kind, configDir: dir, out: io.out,` and before `ok := runDoctor(` add:

```go
		kind, store, err := openStore(dir)
		if err != nil {
			fmt.Fprintf(io.out, "FAIL %-14s %s\n", "secrets", err)
			return 1
		}
```

In `runDoctor`, as the first line after `report` is defined: `report(true, "secrets", string(d.storeKind))`.

`serve.go`: replace `p := newProvider(secrets.NewKeychain(secrets.ServiceName()), dir)` with

```go
		kind, store, err := openStore(dir)
		if err != nil {
			logger.Error("secret store unavailable; every tool call will report it", "err", err)
			store = failingStore{err: err}
		}
		p := newProvider(store, dir)
```

and add `"secrets", kind` to the `minuar-multi-gmail serve` startup log line.

`setup.go` still builds `secrets.NewKeychain` directly; Task 6 replaces it. Keep the `secrets` import in files that still use it and drop it where the compiler says it is unused.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`. The integration test now runs with the file store on every system.

- [ ] **Step 6: Commit**

```bash
git add cmd/minuar-multi-gmail/store.go cmd/minuar-multi-gmail/store_test.go cmd/minuar-multi-gmail/addaccount.go cmd/minuar-multi-gmail/accounts.go cmd/minuar-multi-gmail/doctor.go cmd/minuar-multi-gmail/serve.go cmd/minuar-multi-gmail/commands_test.go cmd/minuar-multi-gmail/serve_integration_test.go
git commit -m "feat(cmd): open the recorded secret store in every command" -m "openStore reads MINUAR_MULTI_GMAIL_SECRETS, then the kind setup recorded; an empty kind means the Keychain on macOS and an error elsewhere. serve keeps starting with a store that reports the error on every call, so Claude Code shows the message instead of a dead server. doctor names the store first."
```

---

### Task 6: setup chooses, explains, and records the store

**Files:**
- Modify: `cmd/minuar-multi-gmail/setup.go` (whole file)
- Modify: `cmd/minuar-multi-gmail/commands_test.go:33-59` (`TestRunSetupStoresCredsAndTellsToDelete`) and add two tests

**Interfaces:**
- Consumes: `secrets.Detect`, `secrets.OpenOn`, `secrets.Kind.Describe`, `config.Load`, `config.Save`, `config.File.Secrets`, `googleauth.ParseClientSecretFile`, `googleauth.SaveClientCreds`.
- Produces: `type setupDeps struct { configDir, goos, envKind string; detect func(service string) (secrets.Kind, string); open func(kind secrets.Kind) (secrets.Store, error) }`, `func runSetup(d setupDeps, path string, out io.Writer) error`, `var errFileStoreNotAccepted`.

- [ ] **Step 1: Rewrite the setup tests**

Replace `TestRunSetupStoresCredsAndTellsToDelete` in `commands_test.go` with:

```go
// setupWith builds setupDeps whose detect returns kind and why, and whose
// open hands out the given store for that kind.
func setupWith(t *testing.T, dir, goos, envKind string, kind secrets.Kind, why string, st secrets.Store) setupDeps {
	t.Helper()
	return setupDeps{
		configDir: dir, goos: goos, envKind: envKind,
		detect: func(string) (secrets.Kind, string) { return kind, why },
		open: func(k secrets.Kind) (secrets.Store, error) {
			if k != kind {
				t.Fatalf("open(%q), want %q", k, kind)
			}
			return st, nil
		},
	}
}

func TestRunSetupStoresCredsAndRecordsTheStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client_secret_x.json")
	os.WriteFile(path, []byte(sampleClientSecret), 0o600)
	st := secrets.NewMem()
	var out bytes.Buffer
	d := setupWith(t, dir, "darwin", "", secrets.KindKeychain, "Secrets go to the macOS Keychain.", st)
	if err := runSetup(d, path, &out); err != nil {
		t.Fatal(err)
	}
	id, _ := st.Get(secrets.ClientIDKey)
	sec, _ := st.Get(secrets.ClientSecretKey)
	if id != "123-abc.apps.googleusercontent.com" || sec != "GOCSPX-s3cr3t_Value-1" {
		t.Fatalf("stored id=%q secret=%q", id, sec)
	}
	cfg, err := config.Load(dir)
	if err != nil || cfg.Secrets != "keychain" {
		t.Fatalf("config = %+v, %v; setup must record the store", cfg, err)
	}
	for _, want := range []string{"Secrets go to the macOS Keychain.", "macOS Keychain", "rm " + path, "Next: minuar-multi-gmail add-account <alias>"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out lacks %q: %q", want, out.String())
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("setup must not delete the file itself")
	}
	if err := runSetup(d, filepath.Join(dir, "missing.json"), &out); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestRunSetupKeepsExistingAccountsWhenRecordingTheStore(t *testing.T) {
	dir := t.TempDir()
	if err := config.Save(dir, &config.File{Version: 1, Default: "work", Accounts: []config.Account{{Alias: "work", Email: "you@company.example", AddedAt: fixedNow()}}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "client_secret_x.json")
	os.WriteFile(path, []byte(sampleClientSecret), 0o600)
	d := setupWith(t, dir, "darwin", "", secrets.KindKeychain, "Secrets go to the macOS Keychain.", secrets.NewMem())
	if err := runSetup(d, path, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(dir)
	if cfg.Secrets != "keychain" || cfg.Default != "work" || len(cfg.Accounts) != 1 {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestRunSetupFileStoreNeedsAcceptanceOnLinux(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client_secret_x.json")
	os.WriteFile(path, []byte(sampleClientSecret), 0o600)
	st := secrets.NewMem()
	why := "No keyring found: secret-tool is not installed."

	var out bytes.Buffer
	d := setupWith(t, dir, "linux", "", secrets.KindFile, why, st)
	err := runSetup(d, path, &out)
	if !errors.Is(err, errFileStoreNotAccepted) {
		t.Fatalf("err = %v, want errFileStoreNotAccepted", err)
	}
	if !strings.Contains(out.String(), why) {
		t.Fatalf("the reason must be printed: %q", out.String())
	}
	if !strings.Contains(err.Error(), secrets.EnvKind+"=file") {
		t.Fatalf("the error must say how to accept: %v", err)
	}
	if _, err := st.Get(secrets.ClientIDKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("nothing may be stored before acceptance")
	}
	if cfg, _ := config.Load(dir); cfg.Secrets != "" {
		t.Fatalf("nothing may be recorded before acceptance: %+v", cfg)
	}

	// Accepted through the environment.
	out.Reset()
	d = setupWith(t, dir, "linux", "file", secrets.KindFile, "Secret store forced to file by "+secrets.EnvKind+".", st)
	if err := runSetup(d, path, &out); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Load(dir); cfg.Secrets != "file" {
		t.Fatalf("config = %+v", cfg)
	}
	if !strings.Contains(out.String(), "file "+filepath.Join(dir, secrets.FileName)) {
		t.Fatalf("out must name the file: %q", out.String())
	}

	// Windows has no other store, so no acceptance step.
	out.Reset()
	d = setupWith(t, t.TempDir(), "windows", "", secrets.KindFile, "This version has no keyring store for Windows.", secrets.NewMem())
	if err := runSetup(d, path, &out); err != nil {
		t.Fatalf("windows: %v", err)
	}
}

func TestRunSetupUnknownEnvKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client_secret_x.json")
	os.WriteFile(path, []byte(sampleClientSecret), 0o600)
	d := setupWith(t, dir, "linux", "vault", "", `unknown secret store "vault"`, secrets.NewMem())
	if err := runSetup(d, path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/... -run RunSetup`
Expected: build failure, `undefined: setupDeps`, `undefined: errFileStoreNotAccepted`.

- [ ] **Step 3: Rewrite `cmd/minuar-multi-gmail/setup.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	kind, why := d.detect(secrets.ServiceName())
	if kind == "" {
		return errors.New(why)
	}
	fmt.Fprintln(out, why)
	if kind == secrets.KindFile && d.goos != "windows" && d.envKind == "" {
		return errFileStoreNotAccepted
	}
	store, err := d.open(kind)
	if err != nil {
		return err
	}
	if err := googleauth.SaveClientCreds(store, creds); err != nil {
		return fmt.Errorf("store in %s: %w", kind.Describe(d.configDir), err)
	}
	cfg, err := config.Load(d.configDir)
	if err != nil {
		return err
	}
	cfg.Secrets = string(kind)
	if err := config.Save(d.configDir, cfg); err != nil {
		return fmt.Errorf("record the secret store: %w", err)
	}
	fmt.Fprintf(out, "Stored OAuth client %s in the %s.\nDelete the downloaded file now:\n  rm %s\nNext: minuar-multi-gmail add-account <alias>\n",
		creds.ID, kind.Describe(d.configDir), path)
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `./scripts/verify.sh`
Expected: `verify: ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/minuar-multi-gmail/setup.go cmd/minuar-multi-gmail/commands_test.go
git commit -m "feat(setup): choose the secret store per system and record it" -m "setup prints why a store was chosen, refuses the file store on Linux until MINUAR_MULTI_GMAIL_SECRETS=file accepts it, and writes the kind into accounts.json. Existing accounts in that file are kept."
```

---

### Task 7: Portability of tests and docs

**Files:**
- Modify: `internal/config/config_test.go:50-66`
- Modify: `internal/tools/handlers_attachment_test.go:60-71`
- Modify: `internal/tools/handlers_attachment.go:26-27, 74-76` (comments)
- Modify: `README.md:14-18, 25-34, 36-38, 55, 90-91, 96-98, 110, 133-138`
- Modify: `docs/superpowers/specs/2026-09-03-multi-gmail-design.md`: no change (historical).

**Interfaces:** none.

- [ ] **Step 1: Skip the mode assertions on Windows**

In `internal/config/config_test.go`, in the test that checks `0o700` and `0o600` (lines 50-66), add as its first statement:

```go
	if runtime.GOOS == "windows" {
		t.Skip("file modes are Unix only")
	}
```

and add `"runtime"` to the imports. Do the same in `internal/tools/handlers_attachment_test.go` inside `TestGetAttachmentSavesTheNamedFile`, just before the `info, err := os.Stat(f.Path)` line, wrapping the two mode checks:

```go
	if runtime.GOOS != "windows" {
		info, err := os.Stat(f.Path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v err %v", info.Mode().Perm(), err)
		}
		dirInfo, err := os.Stat(filepath.Dir(f.Path))
		if err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v err %v", dirInfo.Mode().Perm(), err)
		}
	}
```

- [ ] **Step 2: Verify the whole suite still passes and cross-compiles**

Run: `./scripts/verify.sh && GOOS=linux go vet ./... && GOOS=windows go vet ./... && echo cross-ok`
Expected: `verify: ok` then `cross-ok`.

- [ ] **Step 3: Update comments in the attachment handler**

`internal/tools/handlers_attachment.go`: change "well under the 255 byte limit of a file name on macOS" to "well under the 255 byte file name limit of the common file systems", and "compared without case because the default macOS file system ignores it" to "compared without case because the default macOS and Windows file systems ignore it".

- [ ] **Step 4: Update the README**

Make these edits:

a. Lines 16-18: replace "It runs on your Mac, talks only to Google, and keeps every secret in the macOS Keychain." with "It runs on your own machine (macOS or Linux), talks only to Google, and keeps every secret in the system's keyring."

b. In "How it keeps you safe", replace the "Tokens and the OAuth client live in the Keychain." bullet with: "Tokens and the OAuth client live in the macOS Keychain or the Linux Secret Service. On a machine without a keyring, `setup` offers a file store and stores nothing until you accept it with `MINUAR_MULTI_GMAIL_SECRETS=file`; that file is readable by anyone with your user account or root. The config file holds only aliases, addresses, the sentences you wrote, and which store is in use."

c. Line 37: "You need macOS, Go 1.26" becomes "You need macOS or Linux, Go 1.26".

d. Line 55: "Then delete the downloaded file; the Keychain has it now." becomes "Then delete the downloaded file; the secret store has it now. `setup` prints which store it chose and why."

e. Lines 90-91: "delete it from the Keychain" becomes "delete it from the secret store"; "Check the Keychain, the config" becomes "Check the secret store, the config".

f. Line 96-98: after the config file sentence, add: "`secrets.json` next to it exists only with the file store."

g. Line 110: "to files on your Mac" becomes "to files on your machine".

h. Replace the "## Why macOS only" section with:

```markdown
## Systems

| System | Secret store | Login |
|--------|--------------|-------|
| macOS | Keychain, through the `security` command. | `open` starts the browser. |
| Linux desktop | Secret Service (GNOME Keyring, KWallet) through `secret-tool`. Install `libsecret-tools` (Debian, Ubuntu) or `libsecret` (Fedora, Arch). | `xdg-open` starts the browser. |
| Linux server | No keyring answers, so `setup` offers `secrets.json` in the config directory, mode 0600. Accept it with `MINUAR_MULTI_GMAIL_SECRETS=file` on the `setup` call. | No browser: `add-account` prints the login URL; open it from another machine with an SSH port forward to the port it names. |
| Windows | Compiles and uses `secrets.json`. Not tested. | `rundll32` starts the browser. |

`setup` probes the system once, says which store it chose and why, and
records the choice in `accounts.json`. Every later command uses that store
and reports when it cannot reach it. `MINUAR_MULTI_GMAIL_SECRETS` forces a
store for every command.
```

- [ ] **Step 5: Commit**

```bash
git add internal/config/config_test.go internal/tools/handlers_attachment_test.go internal/tools/handlers_attachment.go README.md
git commit -m "docs: describe the secret store per system; skip mode tests on Windows" -m "The README replaces 'Why macOS only' with a table per system, states the file store's exposure, and names the acceptance step. File mode assertions skip on Windows, where Go's Chmod sets only the read-only flag."
```

---

### Task 8: Verify on Linux

**Files:** none changed. Output: a report in the final message.

**Interfaces:** none.

- [ ] **Step 1: Start Docker**

Run: `open -a Docker` then poll `docker info --format '{{.ServerVersion}}'` every 5 seconds for up to 2 minutes until it prints a version.

- [ ] **Step 2: Run the test suite on Linux**

Run from the repo root:

```bash
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.26 sh -c 'go vet ./... && go test -race ./... 2>&1 | tail -12'
```

Expected: `ok` for every package. Record the output.

- [ ] **Step 3: Run setup on Linux without a keyring**

```bash
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.26 sh -c '
go build -o /tmp/mmg ./cmd/minuar-multi-gmail
mkdir -p /tmp/cfg
printf "%s" "{\"installed\":{\"client_id\":\"123-abc.apps.googleusercontent.com\",\"client_secret\":\"GOCSPX-s3cr3t_Value-1\"}}" > /tmp/cs.json
export MINUAR_MULTI_GMAIL_CONFIG_DIR=/tmp/cfg
echo "--- setup without acceptance (expect exit 1)"; /tmp/mmg setup /tmp/cs.json; echo "exit=$?"
echo "--- setup with acceptance"; MINUAR_MULTI_GMAIL_SECRETS=file /tmp/mmg setup /tmp/cs.json; echo "exit=$?"
echo "--- config"; cat /tmp/cfg/accounts.json; ls -la /tmp/cfg
echo "--- doctor (expect secrets line, then the config line)"; /tmp/mmg doctor; echo "exit=$?"
'
```

Expected: first setup prints "No keyring found: secret-tool is not installed." and exits 1 with nothing in `/tmp/cfg`; second records `"secrets": "file"`, `secrets.json` has mode `-rw-------`; `doctor` starts with `ok   secrets        file`. Record the output.

- [ ] **Step 4: Try the Secret Service in the container**

```bash
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.26 sh -c '
apt-get update -qq >/dev/null && apt-get install -y -qq libsecret-tools gnome-keyring dbus >/dev/null 2>&1
go build -o /tmp/mmg ./cmd/minuar-multi-gmail
mkdir -p /tmp/cfg2
printf "%s" "{\"installed\":{\"client_id\":\"123-abc.apps.googleusercontent.com\",\"client_secret\":\"GOCSPX-s3cr3t_Value-1\"}}" > /tmp/cs.json
export MINUAR_MULTI_GMAIL_CONFIG_DIR=/tmp/cfg2
dbus-run-session -- sh -c "
  echo -n \"\" | gnome-keyring-daemon --unlock --components=secrets >/dev/null 2>&1
  export \$(echo -n \"\" | gnome-keyring-daemon --start --components=secrets 2>/dev/null | tr \"\\n\" \" \")
  echo \"--- setup (expect secret-service)\"; /tmp/mmg setup /tmp/cs.json; echo \"exit=\$?\"
  echo \"--- config\"; cat /tmp/cfg2/accounts.json
  echo \"--- doctor\"; /tmp/mmg doctor; echo \"exit=\$?\"
  echo \"--- secret-tool sees the item\"; secret-tool lookup service com.minuar.multi-gmail account oauth-client-id; echo
"
'
```

Expected when it works: setup prints "Secrets go to the Secret Service keyring", the config records `secret-service`, `doctor` starts with `ok   secrets        secret-service`, and `secret-tool lookup` prints the client id. When the daemon does not start in the container, record the exact error and report the `secret-service` kind as verified through the fake runner only.

- [ ] **Step 5: Report**

State for each of s1 (macOS, the existing tests and the `verify.sh` run), s2 (secret-service), s3 (file on Linux), s4 (Windows cross-compile) what ran and what the output was. No commit.
