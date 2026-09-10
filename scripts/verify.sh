#!/usr/bin/env bash
# Verify gate: formatting, vet, build, tests. Exit non-zero on any failure.
set -euo pipefail
cd "$(dirname "$0")/.."

unformatted="$(gofmt -l ./cmd ./internal 2>/dev/null || true)"
if [ -n "$unformatted" ]; then
  echo "gofmt: files need formatting:" >&2
  echo "$unformatted" >&2
  exit 1
fi
go vet ./...
go build ./...
go test -race ./...
echo "verify: ok"
