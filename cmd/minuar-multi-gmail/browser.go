package main

import (
	"os/exec"
	"runtime"
	"time"
)

// browserStartGrace is how long openBrowser watches the opener for an
// early exit. open and xdg-open return within it on success; a browser
// that runs in the foreground (xdg-open's generic path, a text browser
// over SSH) is left running so the login does not wait for it to close.
const browserStartGrace = 2 * time.Second

// openBrowser opens url with the system's own opener. add-account prints
// the URL before calling this, so a failure here is not fatal: the login
// keeps waiting for the callback.
func openBrowser(url string) error { return startBrowser(browserCommand(runtime.GOOS, url)) }

// startBrowser starts cmd and returns its error only when it fails within
// browserStartGrace. A process still running after that is reaped in the
// background.
func startBrowser(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(browserStartGrace):
		return nil
	}
}

func browserCommand(goos, url string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return exec.Command("xdg-open", url)
	}
}
