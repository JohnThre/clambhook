#!/bin/sh
set -e

echo "Running go vet..."
go vet ./...

echo "Running staticcheck (if installed)..."
if command -v staticcheck >/dev/null 2>&1; then
    staticcheck ./...
else
    echo "staticcheck not found, skipping"
fi

echo "Lint complete."
