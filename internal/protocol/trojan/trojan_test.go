package trojan

import (
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

func TestDialerRegistered(t *testing.T) {
	d, err := protocol.NewDialer(protocol.Server{
		Name:     "test",
		Address:  "example.com:443",
		Protocol: "trojan",
		Settings: map[string]any{"password": "hunter2"},
	})
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	if d.Protocol() != "trojan" {
		t.Errorf("Protocol() = %q, want trojan", d.Protocol())
	}
	if _, ok := d.(protocol.PacketDialer); !ok {
		t.Error("dialer does not implement PacketDialer")
	}
}
