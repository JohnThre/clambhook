package wireguard

import (
	"context"
	"errors"
	"io"

	"github.com/clambhook/clambhook/internal/protocol"
)

func init() {
	protocol.Register("wireguard", func(server protocol.Server) (protocol.Dialer, error) {
		return &dialer{server: server}, nil
	})
}

type dialer struct {
	server protocol.Server
}

func (d *dialer) Protocol() string { return "wireguard" }

func (d *dialer) Dial(ctx context.Context, network, address string) (protocol.Conn, error) {
	return nil, errors.New("wireguard: not implemented")
}

func (d *dialer) DialThrough(ctx context.Context, underlying io.ReadWriteCloser, address string) (protocol.Conn, error) {
	return nil, errors.New("wireguard: not implemented")
}
