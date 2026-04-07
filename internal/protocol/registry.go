package protocol

import (
	"fmt"
	"sync"
)

var (
	mu       sync.RWMutex
	registry = make(map[string]DialerFactory)
)

// Register makes a protocol available by name.
func Register(name string, factory DialerFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = factory
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
