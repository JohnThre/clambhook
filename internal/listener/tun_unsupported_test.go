//go:build !linux

package listener

import (
	"context"
	"strings"
	"testing"
)

func TestUnsupportedTUNStartFailsClearly(t *testing.T) {
	l := NewTUN(TUNOptions{Name: "clambhook-test0"}, nil)
	err := l.Start(context.Background())
	if err == nil {
		t.Fatal("Start returned nil, want unsupported-platform error")
	}
	if !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("Start error = %q", err)
	}
}
