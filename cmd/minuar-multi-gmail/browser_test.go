package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
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

// A browser started in the foreground (xdg-open's generic path, a text
// browser over SSH) must not hold the login until it exits.
func TestStartBrowserReturnsWhileTheBrowserRuns(t *testing.T) {
	start := time.Now()
	if err := startBrowser(exec.Command("sleep", "5")); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("startBrowser waited for the process to exit")
	}
}

func TestStartBrowserReportsAnEarlyFailure(t *testing.T) {
	if err := startBrowser(exec.Command("false")); err == nil {
		t.Fatal("an opener that exits non-zero at once must be reported")
	}
	if err := startBrowser(exec.Command("/nonexistent/opener")); err == nil {
		t.Fatal("an opener that cannot start must be reported")
	}
}
