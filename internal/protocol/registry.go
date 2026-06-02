package protocol

import (
	"fmt"
	"sync"
)

var (
	mu                   sync.RWMutex
	registry             = make(map[string]DialerFactory)
	capabilitiesRegistry = make(map[string]Capabilities)
)

// Register makes a protocol available by name.
func Register(name string, factory DialerFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = factory
}

// RegisterCapabilities publishes static protocol capabilities for inventory
// and UI surfaces that should not instantiate configured dialers.
func RegisterCapabilities(name string, caps Capabilities) {
	mu.Lock()
	defer mu.Unlock()
	capabilitiesRegistry[name] = normalizeCapabilities(caps)
}

// CapabilitiesForProtocol returns static capabilities for a registered
// protocol name. Unknown protocols are reported as TCP-only so inventory
// surfaces stay best-effort while runtime validation remains authoritative.
func CapabilitiesForProtocol(name string) Capabilities {
	mu.RLock()
	defer mu.RUnlock()
	if caps, ok := capabilitiesRegistry[name]; ok {
		return caps
	}
	return Capabilities{
		TCP:       true,
		UDPMode:   UDPModeUnsupported,
		UDPReason: "UDP support is unknown for this protocol",
	}
}

// NewDialer creates a Dialer for the named protocol.
func NewDialer(server Server) (Dialer, error) {
	mu.RLock()
	defer mu.RUnlock()
	factory, ok := registry[server.Protocol]
	if !ok {
		return nil, fmt.Errorf("unknown protocol: %s", server.Protocol)
	}
	return factory(server)
}

// Registered returns a list of registered protocol names.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
