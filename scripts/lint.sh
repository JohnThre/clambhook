#!/bin/sh
set -e

echo "Running go vet..."
go vet ./...

STATICCHECK=${STATICCHECK:-staticcheck}
if ! command -v "$STATICCHECK" >/dev/null 2>&1; then
    GOPATH_BIN=$(go env GOPATH)/bin/staticcheck
    if [ -x "$GOPATH_BIN" ]; then
        STATICCHECK=$GOPATH_BIN
    fi
fi

if ! command -v "$STATICCHECK" >/dev/null 2>&1; then
    echo "staticcheck not found; install it with:" >&2
    echo "  go install honnef.co/go/tools/cmd/staticcheck@2025.1.1" >&2
    exit 1
fi

echo "Running staticcheck..."
"$STATICCHECK" ./...

echo "Lint complete."
