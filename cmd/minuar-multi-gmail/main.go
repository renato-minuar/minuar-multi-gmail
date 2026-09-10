// Command minuar-multi-gmail serves two Gmail accounts to Claude Code over MCP and
// manages the account logins that make that possible.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
)

const version = "0.1.0"

// stdio carries the writers a command may write to. Commands never write
// to os.Stdout directly: `serve` owns stdout for the MCP protocol.
type stdio struct {
	out io.Writer
	err io.Writer
}

type commandFunc func(ctx context.Context, args []string, io stdio) int

// commands is filled by init() functions in the sibling files.
var commands = map[string]commandFunc{}

// commandHelp is the one-line description printed by usage, keyed like commands.
var commandHelp = map[string]string{
	"version": "print the version",
}

func init() {
	commands["version"] = func(_ context.Context, _ []string, io stdio) int {
		fmt.Fprintf(io.out, "minuar-multi-gmail %s\n", version)
		return 0
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: minuar-multi-gmail <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	names := make([]string, 0, len(commandHelp))
	for name := range commandHelp {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-16s %s\n", name, commandHelp[name])
	}
}

func run(ctx context.Context, args []string, io stdio) int {
	if len(args) == 0 {
		usage(io.err)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(io.err, "unknown command %q\n\n", args[0])
		usage(io.err)
		return 2
	}
	return cmd(ctx, args[1:], io)
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], stdio{out: os.Stdout, err: os.Stderr}))
}
