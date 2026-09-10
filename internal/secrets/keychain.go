package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// runResult is what one invocation of `security` produced.
type runResult struct {
	stdout string
	stderr string
	exit   int
	err    error // failure to start the process
}

type keychainStore struct {
	service string
	run     func(args []string, stdin string) runResult
}

// NewKeychain returns a Store backed by the macOS Keychain under service.
func NewKeychain(service string) Store {
	return &keychainStore{service: service, run: runSecurity}
}

func runSecurity(args []string, stdin string) runResult {
	cmd := exec.Command("security", args...)
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

// notFound checks if the Keychain item was not found. security returns OSStatus
// values truncated to 8 bits; errSecItemNotFound (-25300) becomes exit code 44.
func notFound(r runResult) bool {
	return r.exit == 44
}

func (k *keychainStore) fail(op string, r runResult) error {
	if r.err != nil {
		return fmt.Errorf("security %s: %w", op, r.err)
	}
	return fmt.Errorf("security %s failed (exit %d): %s", op, r.exit, strings.TrimSpace(r.stderr))
}

func (k *keychainStore) Get(name string) (string, error) {
	r := k.run([]string{"find-generic-password", "-s", k.service, "-a", name, "-w"}, "")
	if r.err != nil || r.exit != 0 {
		if notFound(r) {
			return "", ErrNotFound
		}
		return "", k.fail("find-generic-password", r)
	}
	return strings.TrimRight(r.stdout, "\r\n"), nil
}

// Set writes through `security -i` so the secret travels on stdin, then
// reads the item back and compares, because interactive mode is the one
// path whose exit status we do not want to trust blindly.
func (k *keychainStore) Set(name, secret string) error {
	if !ValidSecret(secret) {
		return fmt.Errorf("secret for %s contains characters outside the allowed set", name)
	}
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", k.service, name, secret)
	r := k.run([]string{"-i"}, line)
	if r.err != nil || r.exit != 0 {
		return k.fail("add-generic-password", r)
	}
	got, err := k.Get(name)
	if err != nil {
		return fmt.Errorf("read back %s after write: %w", name, err)
	}
	if got != secret {
		return fmt.Errorf("read back %s after write: stored value differs", name)
	}
	return nil
}

func (k *keychainStore) Delete(name string) error {
	r := k.run([]string{"delete-generic-password", "-s", k.service, "-a", name}, "")
	if r.err != nil || r.exit != 0 {
		if notFound(r) {
			return ErrNotFound
		}
		return k.fail("delete-generic-password", r)
	}
	return nil
}
