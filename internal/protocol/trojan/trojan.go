package trojan

import (
	"github.com/JohnThre/clambhook/internal/protocol"
	"github.com/JohnThre/clambhook/internal/protocol/trojanwire"
)

func init() {
	protocol.Register("trojan", func(server protocol.Server) (protocol.Dialer, error) {
		return trojanwire.NewDialer("trojan", server)
	})
}
