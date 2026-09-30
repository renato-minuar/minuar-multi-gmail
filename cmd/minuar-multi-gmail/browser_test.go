package main

import (
	"strings"
	"testing"
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
