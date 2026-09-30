package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	if store.stdin != "tok" {
		t.Fatalf("secret must travel on stdin without a newline, got %q", store.stdin)
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

// A locked keyring makes secret-tool wait on an unlock prompt the user may
// never see (over SSH). The call must give up and say so.
func TestRunSecretToolTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret-tool"), []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := secretToolTimeout
	secretToolTimeout = 200 * time.Millisecond
	t.Cleanup(func() { secretToolTimeout = old })
	start := time.Now()
	r := runSecretTool([]string{"lookup", "service", "x", "account", "y"}, "")
	if r.err == nil || !strings.Contains(r.err.Error(), "timed out") || !strings.Contains(r.err.Error(), "locked") {
		t.Fatalf("runResult = %+v, want a timeout error that mentions a locked keyring", r)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the call waited for the script instead of timing out")
	}
}
