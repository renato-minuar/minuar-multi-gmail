// Package secrets stores OAuth client credentials and refresh tokens.
// Production stores: the macOS Keychain, the Linux Secret Service through secret-tool, and a file for systems without a keyring. setup picks one per system (kind.go).
package secrets

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
)

const (
	DefaultService  = "com.minuar.multi-gmail"
	EnvService      = "MINUAR_MULTI_GMAIL_KEYCHAIN_SERVICE"
	ClientIDKey     = "oauth-client-id"
	ClientSecretKey = "oauth-client-secret"
)

var ErrNotFound = errors.New("secret not found")

// secretRe is the only charset accepted. It needs no quoting in the
// `security` command parser, so secrets can travel on stdin verbatim.
var secretRe = regexp.MustCompile(`^[A-Za-z0-9._~/+=-]+$`)

func RefreshTokenKey(alias string) string { return "refresh-token." + alias }

func ValidSecret(s string) bool { return secretRe.MatchString(s) }

func ServiceName() string {
	if s := os.Getenv(EnvService); s != "" {
		return s
	}
	return DefaultService
}

type Store interface {
	Get(name string) (string, error)
	Set(name, secret string) error
	Delete(name string) error
}

// Mem is an in-memory Store for tests.
type Mem struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMem() *Mem { return &Mem{m: map[string]string{}} }

func (s *Mem) Get(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Mem) Set(name, secret string) error {
	if !ValidSecret(secret) {
		return fmt.Errorf("secret for %s contains characters outside the allowed set", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = secret
	return nil
}

func (s *Mem) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; !ok {
		return ErrNotFound
	}
	delete(s.m, name)
	return nil
}
