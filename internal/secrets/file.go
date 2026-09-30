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
