package clambback

import (
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func TestDialerRegistered(t *testing.T) {
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "test",
		Address:  "example.com:443",
		Protocol: "clambback",
		Settings: map[string]any{"password": "hunter2"},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "clambback" {
		t.Errorf("Protocol() = %q, want clambback", d.Protocol())
	}
	if _, ok := d.(protocol.PacketDialer); !ok {
		t.Error("dialer does not implement PacketDialer")
	}
}

func TestErrorPrefixUsesClambback(t *testing.T) {
	_, err := protocol.NewDialer(protocol.Server{
		Address:  "example.com:443",
		Protocol: "clambback",
		Settings: map[string]any{},
	})
	if err == nil || !strings.Contains(err.Error(), "clambback: password is required") {
		t.Fatalf("NewDialer error = %v, want clambback password error", err)
	}
}
