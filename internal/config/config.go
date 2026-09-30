// Package config stores the list of connected accounts and the default
// alias in ~/.config/minuar-multi-gmail/accounts.json. It never holds secrets; it records which store does.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	FileName = "accounts.json"
	EnvDir   = "MINUAR_MULTI_GMAIL_CONFIG_DIR"
	// maxDescriptionLength bounds an account description: long enough for a
	// sentence, short enough to stay cheap in every tool schema.
	maxDescriptionLength = 200
)

var aliasRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type Account struct {
	Alias string `json:"alias"`
	Email string `json:"email"`
	// Description tells the model when to use this account. It is written
	// by add-account or set-description and rendered into the tool schemas
	// and the server instructions at startup.
	Description string    `json:"description,omitempty"`
	AddedAt     time.Time `json:"added_at"`
}

type File struct {
	Version int    `json:"version"`
	Default string `json:"default,omitempty"`
	// Secrets is the secret store kind chosen by setup: "keychain",
	// "secret-service" or "file". Empty in files written before the
	// choice existed, which every command reads as the macOS Keychain.
	Secrets  string    `json:"secrets,omitempty"`
	Accounts []Account `json:"accounts"`
}

// Dir returns the config directory: $MINUAR_MULTI_GMAIL_CONFIG_DIR when set,
// otherwise ~/.config/minuar-multi-gmail.
func Dir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return filepath.Join(home, ".config", "minuar-multi-gmail"), nil
}

func ValidAlias(alias string) bool { return aliasRe.MatchString(alias) }

// ValidDescription rejects a description longer than maxDescriptionLength
// characters. An empty text is always valid: it clears the description.
func ValidDescription(text string) error {
	if n := utf8.RuneCountInString(text); n > maxDescriptionLength {
		return fmt.Errorf("description is too long (%d characters, maximum %d)", n, maxDescriptionLength)
	}
	return nil
}

// Load reads dir/accounts.json. A missing file yields an empty File.
func Load(dir string) (*File, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w (fix or move the file; it is never overwritten automatically)", path, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("parse %s: unsupported version %d", path, f.Version)
	}
	if f.Accounts == nil {
		f.Accounts = []Account{}
	}
	return &f, nil
}

// Save writes atomically: temp file in the same directory, then rename.
func Save(dir string, f *File) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	if f.Accounts == nil {
		f.Accounts = []Account{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", dir, err)
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
	final := filepath.Join(dir, FileName)
	if err := os.Rename(tmpPath, final); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", final, err)
	}
	return nil
}

func (f *File) Find(alias string) (Account, bool) {
	for _, a := range f.Accounts {
		if a.Alias == alias {
			return a, true
		}
	}
	return Account{}, false
}

func (f *File) Aliases() []string {
	out := make([]string, 0, len(f.Accounts))
	for _, a := range f.Accounts {
		out = append(out, a.Alias)
	}
	sort.Strings(out)
	return out
}

func (f *File) emailOwner(email string) (string, bool) {
	for _, a := range f.Accounts {
		if strings.EqualFold(a.Email, email) {
			return a.Alias, true
		}
	}
	return "", false
}

func (f *File) Add(a Account) error {
	if !ValidAlias(a.Alias) {
		return fmt.Errorf("invalid alias %q: use lowercase letters, digits and dashes, starting with a letter, at most 32 characters", a.Alias)
	}
	if a.Email == "" {
		return errors.New("account email is empty")
	}
	if _, exists := f.Find(a.Alias); exists {
		return fmt.Errorf("alias %q already exists (use --replace to re-login)", a.Alias)
	}
	if owner, exists := f.emailOwner(a.Email); exists {
		return fmt.Errorf("email %s is already connected as alias %q", a.Email, owner)
	}
	f.Accounts = append(f.Accounts, a)
	if f.Default == "" {
		f.Default = a.Alias
	}
	return nil
}

func (f *File) Replace(a Account) error {
	idx := -1
	for i, existing := range f.Accounts {
		if existing.Alias == a.Alias {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("alias %q does not exist", a.Alias)
	}
	if owner, exists := f.emailOwner(a.Email); exists && owner != a.Alias {
		return fmt.Errorf("email %s is already connected as alias %q", a.Email, owner)
	}
	f.Accounts[idx] = a
	return nil
}

func (f *File) Remove(alias string) error {
	for i, a := range f.Accounts {
		if a.Alias == alias {
			f.Accounts = append(f.Accounts[:i], f.Accounts[i+1:]...)
			if f.Default == alias {
				f.Default = ""
			}
			return nil
		}
	}
	return fmt.Errorf("alias %q does not exist", alias)
}

func (f *File) SetDefault(alias string) error {
	if _, ok := f.Find(alias); !ok {
		return fmt.Errorf("alias %q does not exist (known: %s)", alias, strings.Join(f.Aliases(), ", "))
	}
	f.Default = alias
	return nil
}

// SetDescription replaces the description of one account. An empty text
// clears it.
func (f *File) SetDescription(alias, text string) error {
	for i, a := range f.Accounts {
		if a.Alias == alias {
			f.Accounts[i].Description = text
			return nil
		}
	}
	return fmt.Errorf("alias %q does not exist (known: %s)", alias, strings.Join(f.Aliases(), ", "))
}

// Resolve maps a tool's account argument to an account. Empty means default.
func (f *File) Resolve(alias string) (Account, error) {
	if len(f.Accounts) == 0 {
		return Account{}, errors.New("no accounts connected: run minuar-multi-gmail add-account <alias>")
	}
	if alias == "" {
		if f.Default == "" {
			return Account{}, fmt.Errorf("no default account set: run minuar-multi-gmail set-default <alias> (known: %s)", strings.Join(f.Aliases(), ", "))
		}
		alias = f.Default
	}
	a, ok := f.Find(alias)
	if !ok {
		return Account{}, fmt.Errorf("unknown account %q (known: %s)", alias, strings.Join(f.Aliases(), ", "))
	}
	return a, nil
}
