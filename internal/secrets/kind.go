package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// probeSecretTool writes, reads back and clears a throwaway item so the
// choice rests on a working keyring, not on the command being installed.
// When the write works but the clear fails, a probe item may stay in the
// keyring; the error names it.
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
	got, err := st.Get(name)
	if err != nil {
		return fmt.Errorf("read back probe item %s: %w", name, err)
	}
	if got != "probe" {
		return errors.New("read back probe item " + name + ": stored value differs")
	}
	if err := st.Delete(name); err != nil {
		return fmt.Errorf("the keyring accepted the write but could not clear the probe item %s: %w", name, err)
	}
	return nil
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
