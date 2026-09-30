#!/usr/bin/env bash
# Build the binary into ~/.local/bin and print the one-time Claude Code
# registration command.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/minuar-multi-gmail" ./cmd/minuar-multi-gmail
echo "installed: $HOME/.local/bin/minuar-multi-gmail ($("$HOME/.local/bin/minuar-multi-gmail" version))"
if [ -t 0 ]; then
  exec "$HOME/.local/bin/minuar-multi-gmail" wizard
fi
echo
echo "Run the wizard from a terminal to finish: $HOME/.local/bin/minuar-multi-gmail wizard"
echo "Or register by hand: claude mcp add --scope user gmail -- $HOME/.local/bin/minuar-multi-gmail serve"
