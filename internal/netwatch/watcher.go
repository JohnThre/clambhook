// Package netwatch detects the active network interface and SSID, polling
// for changes so the engine can auto-switch profiles when the physical
// network changes (e.g., joining a known SSID or plugging into Ethernet).
package netwatch

import (
	"context"
	"log"
	"strings"
	"time"
)

const defaultPollInterval = 10 * time.Second

// NetworkInfo describes the currently active network connection.
type NetworkInfo struct {
	InterfaceName string
	SSID          string
	IsWiFi        bool
}

// Watcher polls the network state and emits on changes.
type Watcher struct {
	interval time.Duration
}

// New creates a Watcher with the default poll interval.
func New() *Watcher {
	return &Watcher{interval: defaultPollInterval}
}

// Watch returns a channel that emits NetworkInfo whenever the network
// changes. The first value is emitted immediately. The channel is closed
// when ctx is done.
func (w *Watcher) Watch(ctx context.Context) <-chan NetworkInfo {
	ch := make(chan NetworkInfo, 1)
	go func() {
		defer close(ch)
		last := NetworkInfo{}
		first := true
		interval := w.interval
		if interval <= 0 {
			interval = defaultPollInterval
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if !first {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
			first = false
			info, err := current()
			if err != nil {
				log.Printf("netwatch: %v", err)
				continue
			}
			if info != last {
				last = info
				select {
				case ch <- info:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch
}

// MatchesTrigger reports whether info matches a trigger defined by SSID or
// interface name. Empty trigger fields are wildcards; both empty returns false.
func (info NetworkInfo) MatchesTrigger(ssid, iface string) bool {
	ssid = strings.TrimSpace(ssid)
	iface = strings.TrimSpace(iface)
	if ssid == "" && iface == "" {
		return false
	}
	if ssid != "" && !strings.EqualFold(info.SSID, ssid) {
		return false
	}
	if iface != "" && !strings.EqualFold(info.InterfaceName, iface) {
		return false
	}
	return true
}
