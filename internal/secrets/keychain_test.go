package secrets

import (
	"errors"
	"os"
	"strings"
	"testing"
)

type call struct {
	args  []string
	stdin string
}

type fakeRunner struct {
	calls   []call
	results []runResult // consumed in order
}

func (f *fakeRunner) run(args []string, stdin string) runResult {
	f.calls = append(f.calls, call{args: args, stdin: stdin})
	if len(f.results) == 0 {
		return runResult{}
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r
}

func newFakeKeychain(results ...runResult) (*keychainStore, *fakeRunner) {
	fr := &fakeRunner{results: results}
	return &keychainStore{service: "svc.test", run: fr.run}, fr
}

func TestKeychainSetUsesStdinAndReadsBack(t *testing.T) {
	ks, fr := newFakeKeychain(
		runResult{exit: 0},                     // add-generic-password via -i
		runResult{exit: 0, stdout: "s3cret\n"}, // read back
	)
	if err := ks.Set("refresh-token.work", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fr.calls))
	}
	add := fr.calls[0]
	if strings.Join(add.args, " ") != "-i" {
		t.Fatalf("add args = %v, want [-i]", add.args)
	}
	if add.stdin != "add-generic-password -U -s svc.test -a refresh-token.work -w s3cret\n" {
		t.Fatalf("stdin = %q", add.stdin)
	}
	for _, a := range add.args {
		if strings.Contains(a, "s3cret") {
			t.Fatal("secret leaked into process arguments")
		}
	}
	get := fr.calls[1]
	if strings.Join(get.args, " ") != "find-generic-password -s svc.test -a refresh-token.work -w" {
		t.Fatalf("get args = %v", get.args)
	}
}

func TestKeychainSetFailsWhenReadBackDiffers(t *testing.T) {
	ks, _ := newFakeKeychain(runResult{exit: 0}, runResult{exit: 0, stdout: "other\n"})
	if err := ks.Set("k", "value"); err == nil || !strings.Contains(err.Error(), "read back") {
		t.Fatalf("err = %v, want read-back mismatch", err)
	}
}

func TestKeychainSetRejectsInvalidSecretBeforeRunning(t *testing.T) {
	ks, fr := newFakeKeychain()
	if err := ks.Set("k", "has space"); err == nil {
		t.Fatal("invalid secret accepted")
	}
	if len(fr.calls) != 0 {
		t.Fatal("security must not run for an invalid secret")
	}
}

func TestKeychainSetPropagatesExitStatus(t *testing.T) {
	ks, _ := newFakeKeychain(runResult{exit: 1, stderr: "security: boom"})
	if err := ks.Set("k", "value"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestKeychainGetMissingIsErrNotFound(t *testing.T) {
	ks, _ := newFakeKeychain(runResult{exit: 44, stderr: "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain."})
	_, err := ks.Get("k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestKeychainGetTrimsTrailingNewline(t *testing.T) {
	ks, _ := newFakeKeychain(runResult{exit: 0, stdout: "tok\n"})
	got, err := ks.Get("k")
	if err != nil || got != "tok" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestKeychainDelete(t *testing.T) {
	ks, fr := newFakeKeychain(runResult{exit: 0}, runResult{exit: 44, stderr: "could not be found"})
	if err := ks.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(fr.calls[0].args, " ") != "delete-generic-password -s svc.test -a k" {
		t.Fatalf("args = %v", fr.calls[0].args)
	}
	if err := ks.Delete("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestKeychainGetOtherErrorWithCouldNotBeFound(t *testing.T) {
	// Unrelated Keychain failure with misleading error message must not be ErrNotFound.
	// For example, a broken default Keychain returns exit 1 (not 44) and mentions
	// "could not be found" in the message. We classify on exit code 44 only.
	ks, _ := newFakeKeychain(runResult{exit: 1, stderr: "security: SecKeychainSearchCopyNext: A default keychain could not be found."})
	_, err := ks.Get("k")
	if errors.Is(err, ErrNotFound) {
		t.Fatal("misclassified non-44 exit as ErrNotFound")
	}
	if err == nil {
		t.Fatal("expected non-nil error for exit 1")
	}
}

func TestKeychainDeleteOtherErrorWithCouldNotBeFound(t *testing.T) {
	// Unrelated Keychain failure with misleading error message must not be ErrNotFound.
	ks, _ := newFakeKeychain(runResult{exit: 1, stderr: "security: SecKeychainSearchCopyNext: A default keychain could not be found."})
	err := ks.Delete("k")
	if errors.Is(err, ErrNotFound) {
		t.Fatal("misclassified non-44 exit as ErrNotFound")
	}
	if err == nil {
		t.Fatal("expected non-nil error for exit 1")
	}
}

// TestKeychainIntegration talks to the real macOS Keychain under a throwaway
// service name. Run with: MINUAR_MULTI_GMAIL_KEYCHAIN_TEST=1 go test ./internal/secrets/ -run Integration -v
func TestKeychainIntegration(t *testing.T) {
	if os.Getenv("MINUAR_MULTI_GMAIL_KEYCHAIN_TEST") != "1" {
		t.Skip("set MINUAR_MULTI_GMAIL_KEYCHAIN_TEST=1 to run against the real Keychain")
	}
	const service = "com.minuar.multi-gmail.test"
	ks := NewKeychain(service)
	name := "refresh-token.integration"
	t.Cleanup(func() { ks.Delete(name) })
	if _, err := ks.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pre-state: %v", err)
	}
	if err := ks.Set(name, "1//0gFirst-Value_ABC"); err != nil {
		t.Fatal(err)
	}
	if err := ks.Set(name, "1//0gSecond-Value_XYZ"); err != nil {
		t.Fatalf("update with -U failed: %v", err)
	}
	got, err := ks.Get(name)
	if err != nil || got != "1//0gSecond-Value_XYZ" {
		t.Fatalf("got %q err %v", got, err)
	}
	if err := ks.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
