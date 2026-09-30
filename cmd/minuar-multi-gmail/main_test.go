package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunUnknownCommandPrintsUsage(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run(context.Background(), []string{"bogus"}, stdio{out: &out, err: &errBuf})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), `unknown command "bogus"`) {
		t.Fatalf("stderr = %q, want unknown command message", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Usage: minuar-multi-gmail <command>") {
		t.Fatalf("stderr = %q, want usage", errBuf.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must stay empty, got %q", out.String())
	}
}

func TestRunNoArgsPrintsUsage(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run(context.Background(), nil, stdio{out: &out, err: &errBuf})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "Usage: minuar-multi-gmail <command>") {
		t.Fatalf("stderr = %q, want usage", errBuf.String())
	}
}

func TestRunDispatchesRegisteredCommand(t *testing.T) {
	commands["__test"] = func(_ context.Context, args []string, io stdio) int {
		io.out.Write([]byte("args=" + strings.Join(args, ",")))
		return 7
	}
	t.Cleanup(func() { delete(commands, "__test") })
	var out, errBuf bytes.Buffer
	code := run(context.Background(), []string{"__test", "a", "b"}, stdio{out: &out, err: &errBuf})
	if code != 7 || out.String() != "args=a,b" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run(context.Background(), []string{"version"}, stdio{out: &out, err: &errBuf})
	if code != 0 || strings.TrimSpace(out.String()) != "minuar-multi-gmail "+version {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

// Without a terminal, a bare invocation prints the usage as before; with
// one it would start the wizard (tested in wizard_test.go).
func TestBareInvocationWithoutTerminalPrintsUsage(t *testing.T) {
	prev := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = prev })

	var out, errOut bytes.Buffer
	code := run(context.Background(), nil, stdio{out: &out, err: &errOut})
	if code != 2 || !strings.Contains(errOut.String(), "Usage: minuar-multi-gmail <command>") || !strings.Contains(errOut.String(), "wizard") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}
