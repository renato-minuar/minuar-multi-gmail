#!/usr/bin/env bash
# Build the binary into ~/.local/bin and print the one-time Claude Code
# registration command.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/minuar-multi-gmail" ./cmd/minuar-multi-gmail
echo "installed: $HOME/.local/bin/minuar-multi-gmail ($("$HOME/.local/bin/minuar-multi-gmail" version))"
echo
echo "Register once in Claude Code (user scope, all projects):"
echo "  claude mcp add --scope user gmail -- $HOME/.local/bin/minuar-multi-gmail serve"
echo "Then check with: claude mcp list"
