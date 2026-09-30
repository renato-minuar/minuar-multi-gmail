package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url with the system's own opener. add-account prints
// the URL before calling this, so a failure here is not fatal: the login
// keeps waiting for the callback.
func openBrowser(url string) error { return browserCommand(runtime.GOOS, url).Run() }

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
