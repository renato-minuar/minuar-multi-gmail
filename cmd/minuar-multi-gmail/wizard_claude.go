package main

import (
	"fmt"
	"strings"
)

func manualRegisterCommand(binary string) string {
	return "claude mcp add --scope user gmail -- " + binary + " serve"
}

// wizardClaude is step w2: register the server in Claude Code at user
// scope. Nothing here is fatal; the manual command is always printed when
// the wizard could not do it.
func wizardClaude(d *wizardDeps) {
	heading(d, "Claude Code")
	if _, err := d.lookPath("claude"); err != nil {
		fmt.Fprintf(d.out, "Claude Code is not on PATH. Register later with:\n  %s\n", manualRegisterCommand(d.binary))
		return
	}
	if _, err := d.run("claude", "mcp", "get", "gmail"); err == nil {
		fmt.Fprintln(d.out, "Already registered in Claude Code.")
		return
	}
	out, err := d.run("claude", "mcp", "add", "--scope", "user", "gmail", "--", d.binary, "serve")
	if err != nil {
		fmt.Fprintf(d.out, "Registration failed: %s\nRegister later with:\n  %s\n", strings.TrimSpace(out), manualRegisterCommand(d.binary))
		return
	}
	fmt.Fprintln(d.out, "Registered in Claude Code (user scope, every project).")
}
