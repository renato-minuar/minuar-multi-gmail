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

// Set writes through secret-tool store with the bare secret on stdin (no
// trailing newline: secret-tool stores stdin verbatim), then reads the item
// back and compares, as the Keychain store does.
func (s *secretToolStore) Set(name, secret string) error {
	if !ValidSecret(secret) {
		return fmt.Errorf("secret for %s contains characters outside the allowed set", name)
	}
	args := append([]string{"store", "--label=minuar-multi-gmail " + name}, s.attrs(name)...)
	r := s.run(args, secret)
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
