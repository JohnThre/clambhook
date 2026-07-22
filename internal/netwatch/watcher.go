// Package netwatch detects the active network interface and SSID, polling
// for changes so the engine can auto-switch profiles when the physical
// network changes (e.g., joining a known SSID or plugging into Ethernet).
package netwatch

import (
	"context"
	"errors"
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

// sourceFunc reports the currently active network. It is the injectable seam
// behind Watcher: production uses the platform current() implementation, while
// tests supply a deterministic function.
type sourceFunc func() (NetworkInfo, error)

// Watcher polls the network state and emits on changes.
type Watcher struct {
	interval time.Duration
	source   sourceFunc
}

// New creates a Watcher with the default poll interval backed by the platform
// network probe.
func New() *Watcher {
	return &Watcher{interval: defaultPollInterval, source: current}
}

// newWithSource creates a Watcher with an explicit poll interval and network
// source. It exists so tests can drive Watch deterministically without touching
// platform state.
func newWithSource(interval time.Duration, source sourceFunc) *Watcher {
	return &Watcher{interval: interval, source: source}
}

// Watch returns a channel that emits NetworkInfo whenever the network
// changes. The first value is emitted immediately. The channel is closed
// when ctx is done.
func (w *Watcher) Watch(ctx context.Context) <-chan NetworkInfo {
	ch := make(chan NetworkInfo, 1)
	source := w.source
	if source == nil {
		source = current
	}
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
			// Run the platform probe with a timeout so a hanging subprocess
			// (e.g. scutil, networksetup) cannot block the watcher goroutine
			// indefinitely or leak it past context cancellation.
			info, err := probeWithTimeout(ctx, source, 5*time.Second)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
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

// probeWithTimeout runs source() in a goroutine and returns its result or
// ctx.Err() when either the parent context or the timeout fires. This
// prevents a hanging platform subprocess from blocking the watcher past
// context cancellation.
func probeWithTimeout(ctx context.Context, source sourceFunc, timeout time.Duration) (NetworkInfo, error) {
	type result struct {
		info NetworkInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := source()
		done <- result{info, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.info, r.err
	case <-ctx.Done():
		return NetworkInfo{}, ctx.Err()
	case <-timer.C:
		return NetworkInfo{}, errors.New("netwatch: probe timed out")
	}
}
