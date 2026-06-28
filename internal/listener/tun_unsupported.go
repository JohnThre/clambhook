//go:build !linux && !darwin

package listener

import (
	"context"
	"sync/atomic"

	"github.com/JohnThre/clambhook/internal/chain"
)

// NewTUN returns a listener placeholder on unsupported platforms so configs can
// parse consistently while Start reports the platform limitation clearly.
func NewTUN(opts TUNOptions, _ *chain.Chain) Listener {
	return &unsupportedTUN{name: opts.name()}
}

func NewTUNWithPlanner(opts TUNOptions, _ *chain.Chain, _ RoutePlanner) Listener {
	return &unsupportedTUN{name: opts.name()}
}

func TUNSupported() bool { return false }

type unsupportedTUN struct {
	name   string
	active atomic.Int64
}

func (t *unsupportedTUN) Start(context.Context) error {
	return TUNUnsupportedError()
}

func (t *unsupportedTUN) Stop() error        { return nil }
func (t *unsupportedTUN) Addr() string       { return t.name }
func (t *unsupportedTUN) Protocol() string   { return "tun" }
func (t *unsupportedTUN) ActiveConns() int64 { return t.active.Load() }
