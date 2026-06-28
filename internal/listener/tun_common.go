package listener

import (
	"context"
	"errors"

	"github.com/JohnThre/clambhook/internal/events"
	"github.com/JohnThre/clambhook/internal/policy"
)

const (
	defaultTUNMTU = 1500

	unsupportedTUNError = "tun: device-wide TUN mode is only supported on Linux and macOS"
)

func TUNUnsupportedError() error { return errors.New(unsupportedTUNError) }

// DNSProxy answers raw DNS wire queries for TUN DNS interception.
type DNSProxy interface {
	Exchange(context.Context, []byte) ([]byte, error)
	Close() error
}

// PolicyManager is a lifecycle hook for route planners with background state.
type PolicyManager interface {
	Start(context.Context)
	Close() error
	Snapshot(profile string) policy.Snapshot
}

// TUNOptions tunes the device-wide TUN listener. Platform implementations own
// interface and route changes for the lifetime of the listener.
type TUNOptions struct {
	Name          string
	ProfileName   string
	MTU           int
	Addresses     []string
	Routes        []string
	ExcludeCIDRs  []string
	ChainName     string
	EventBus      *events.Bus
	DNSProxy      DNSProxy
	PolicyManager PolicyManager
}

func (o TUNOptions) name() string {
	if o.Name != "" {
		return o.Name
	}
	return platformDefaultTUNName()
}

func (o TUNOptions) mtu() int {
	if o.MTU > 0 {
		return o.MTU
	}
	return defaultTUNMTU
}
