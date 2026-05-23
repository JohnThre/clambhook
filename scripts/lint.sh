#!/bin/sh
set -e

echo "Running go vet..."
go vet ./...

if ! command -v staticcheck >/dev/null 2>&1; then
    echo "staticcheck not found; install it with:" >&2
    echo "  go install honnef.co/go/tools/cmd/staticcheck@2025.1.1" >&2
    exit 1
fi

echo "Running staticcheck..."
staticcheck ./...

echo "Lint complete."
