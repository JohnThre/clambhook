//go:build !linux

package listener

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/JohnThre/clambhook/internal/chain"
)

// NewTUN returns a listener placeholder on non-Linux platforms so configs can
// parse consistently while Start reports the platform limitation clearly.
func NewTUN(opts TUNOptions, _ *chain.Chain) Listener {
	return &unsupportedTUN{name: opts.name()}
}

type unsupportedTUN struct {
	name   string
	active atomic.Int64
}

func (t *unsupportedTUN) Start(context.Context) error {
	return errors.New("tun: device-wide TUN mode is only supported on Linux")
}

func (t *unsupportedTUN) Stop() error        { return nil }
func (t *unsupportedTUN) Addr() string       { return t.name }
func (t *unsupportedTUN) Protocol() string   { return "tun" }
func (t *unsupportedTUN) ActiveConns() int64 { return t.active.Load() }
