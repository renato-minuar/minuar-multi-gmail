package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func manualRegisterCommand(binary string) string {
	return "claude mcp add --scope user gmail -- " + binary + " serve"
}

// claudeTimeout bounds each claude call: a hung CLI must not hang the wizard.
const claudeTimeout = 45 * time.Second

// wizardClaude is step w2: register the server in Claude Code at user
// scope. Nothing here is fatal; the manual command is always printed when
// the wizard could not do it.
func wizardClaude(ctx context.Context, d *wizardDeps) {
	heading(d, "Claude Code")
	if _, err := d.lookPath("claude"); err != nil {
		fmt.Fprintf(d.out, "Claude Code is not on PATH. Register later with:\n  %s\n", manualRegisterCommand(d.binary))
		return
	}
	getCtx, cancelGet := context.WithTimeout(ctx, claudeTimeout)
	_, err := d.run(getCtx, "claude", "mcp", "get", "gmail")
	cancelGet()
	if err == nil {
		fmt.Fprintln(d.out, "Already registered in Claude Code.")
		return
	}
	addCtx, cancelAdd := context.WithTimeout(ctx, claudeTimeout)
	defer cancelAdd()
	out, err := d.run(addCtx, "claude", "mcp", "add", "--scope", "user", "gmail", "--", d.binary, "serve")
	if err != nil {
		fmt.Fprintf(d.out, "Registration failed: %s (%v)\nRegister later with:\n  %s\n", strings.TrimSpace(out), err, manualRegisterCommand(d.binary))
		return
	}
	fmt.Fprintln(d.out, "Registered in Claude Code (user scope, every project).")
}
