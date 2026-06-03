package listener

import (
	"context"
	"errors"

	"github.com/JohnThre/clambhook/internal/events"
)

const (
	defaultTUNName = "clambhook0"
	defaultTUNMTU  = 1500

	unsupportedTUNError = "tun: device-wide TUN mode is only supported on Linux"
)

func TUNUnsupportedError() error { return errors.New(unsupportedTUNError) }

// DNSProxy answers raw DNS wire queries for TUN DNS interception.
type DNSProxy interface {
	Exchange(context.Context, []byte) ([]byte, error)
	Close() error
}

// TUNOptions tunes the device-wide TUN listener. The Linux implementation
// owns the interface and route changes for the lifetime of the listener.
type TUNOptions struct {
	Name         string
	ProfileName  string
	MTU          int
	Addresses    []string
	Routes       []string
	ExcludeCIDRs []string
	ChainName    string
	EventBus     *events.Bus
	DNSProxy     DNSProxy
}

func (o TUNOptions) name() string {
	if o.Name != "" {
		return o.Name
	}
	return defaultTUNName
}

func (o TUNOptions) mtu() int {
	if o.MTU > 0 {
		return o.MTU
	}
	return defaultTUNMTU
}
