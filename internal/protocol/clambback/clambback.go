// Package clambback implements the outbound client for clambback server mode.
// The wire format is Trojan-compatible: TLS, hex(SHA224(password)) request
// headers, and Trojan-style UDP datagram frames.
package clambback

import (
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/protocol/trojanwire"
)

func init() {
	protocol.Register("clambback", func(server protocol.Server) (protocol.Dialer, error) {
		return trojanwire.NewDialer("clambback", server)
	})
}
