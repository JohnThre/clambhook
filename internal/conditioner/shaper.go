// Package conditioner implements a network conditioner (a.k.a. link
// conditioner or network throttle) that simulates constrained networks by
// shaping the connections a route plan dials. It wraps net.Conn and
// net.PacketConn values with bandwidth caps, added latency/jitter, and — for
// packet connections — probabilistic packet loss.
//
// The conditioner is a single choke point held by the engine: every chain hop
// dialed by a route plan is wrapped uniformly, independent of listener type.
// Configuration is swappable at runtime; each newly wrapped connection reads
// the current snapshot, so a live update takes effect on subsequent dials
// without disturbing in-flight connections.
package conditioner

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// minBurst bounds a limiter's burst so it can always admit a full read/write
// buffer in a single WaitN call; larger requests are split across calls.
const minBurst = 64 * 1024

// Config describes the shaping applied to conditioned connections. It mirrors
// config.ConditionerConfig but uses standard-library primitives so this
// package does not depend on the config package.
type Config struct {
	Enabled      bool
	DownloadKbps int
	UploadKbps   int
	Latency      time.Duration
	Jitter       time.Duration
	LossPercent  float64
}

// active reports whether the config would apply any shaping at all. A disabled
// or empty config is a no-op and connections pass through untouched.
func (c Config) active() bool {
	if !c.Enabled {
		return false
	}
	return c.DownloadKbps > 0 || c.UploadKbps > 0 ||
		c.Latency > 0 || c.Jitter > 0 || c.LossPercent > 0
}

// Shaper wraps connections according to a swappable Config. It is safe for
// concurrent use.
type Shaper struct {
	mu  sync.RWMutex
	cfg Config
}

// New returns a Shaper seeded with cfg.
func New(cfg Config) *Shaper {
	return &Shaper{cfg: cfg}
}

// Update atomically swaps the shaping config. Connections wrapped after the
// update observe the new settings; already-wrapped connections keep their
// original snapshot.
func (s *Shaper) Update(cfg Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// Config returns the current shaping config.
func (s *Shaper) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// snapshot returns the current config, or a zero (inactive) config for a nil
// shaper so callers can wrap unconditionally.
func (s *Shaper) snapshot() Config {
	if s == nil {
		return Config{}
	}
	return s.Config()
}

// WrapConn returns c shaped by the current config, or c unchanged when the
// conditioner is disabled or configured as a no-op.
func (s *Shaper) WrapConn(c net.Conn) net.Conn {
	if c == nil {
		return c
	}
	cfg := s.snapshot()
	if !cfg.active() {
		return c
	}
	return &shapedConn{
		Conn:    c,
		down:    bandwidthLimiter(cfg.DownloadKbps),
		up:      bandwidthLimiter(cfg.UploadKbps),
		latency: cfg.Latency,
		jitter:  cfg.Jitter,
	}
}

// WrapPacketConn returns c shaped by the current config, or c unchanged when
// the conditioner is disabled or configured as a no-op. Packet loss applies
// only to packet connections.
func (s *Shaper) WrapPacketConn(c net.PacketConn) net.PacketConn {
	if c == nil {
		return c
	}
	cfg := s.snapshot()
	if !cfg.active() {
		return c
	}
	return &shapedPacketConn{
		PacketConn: c,
		down:       bandwidthLimiter(cfg.DownloadKbps),
		up:         bandwidthLimiter(cfg.UploadKbps),
		latency:    cfg.Latency,
		jitter:     cfg.Jitter,
		loss:       clampLoss(cfg.LossPercent),
	}
}

// bandwidthLimiter builds a token-bucket limiter for a kbit/s cap. A
// non-positive cap means unlimited and yields a nil limiter.
func bandwidthLimiter(kbps int) *rate.Limiter {
	if kbps <= 0 {
		return nil
	}
	bytesPerSec := float64(kbps) * 1000 / 8
	burst := int(bytesPerSec)
	if burst < minBurst {
		burst = minBurst
	}
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

// clampLoss bounds a percentage into [0, 100] and converts it to a [0, 1]
// probability.
func clampLoss(percent float64) float64 {
	switch {
	case percent <= 0:
		return 0
	case percent >= 100:
		return 1
	default:
		return percent / 100
	}
}

// waitBytes blocks until n tokens are available, splitting requests larger
// than the limiter's burst across multiple waits so WaitN never rejects.
func waitBytes(l *rate.Limiter, n int) {
	if l == nil || n <= 0 {
		return
	}
	burst := l.Burst()
	if burst <= 0 {
		return
	}
	for n > 0 {
		chunk := n
		if chunk > burst {
			chunk = burst
		}
		_ = l.WaitN(context.Background(), chunk)
		n -= chunk
	}
}

// applyDelay sleeps for the configured latency plus a random jitter fraction.
func applyDelay(latency, jitter time.Duration) {
	d := latency
	if jitter > 0 {
		d += time.Duration(rand.Float64() * float64(jitter))
	}
	if d > 0 {
		time.Sleep(d)
	}
}

// shapedConn wraps a net.Conn with per-direction bandwidth limits and added
// latency/jitter.
type shapedConn struct {
	net.Conn
	down    *rate.Limiter
	up      *rate.Limiter
	latency time.Duration
	jitter  time.Duration
}

// Read shapes inbound (download) traffic.
func (c *shapedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		waitBytes(c.down, n)
	}
	applyDelay(c.latency, c.jitter)
	return n, err
}

// Write shapes outbound (upload) traffic.
func (c *shapedConn) Write(b []byte) (int, error) {
	waitBytes(c.up, len(b))
	applyDelay(c.latency, c.jitter)
	return c.Conn.Write(b)
}

// shapedPacketConn wraps a net.PacketConn with bandwidth limits, latency, and
// probabilistic packet loss on outbound datagrams.
type shapedPacketConn struct {
	net.PacketConn
	down    *rate.Limiter
	up      *rate.Limiter
	latency time.Duration
	jitter  time.Duration
	loss    float64
}

// ReadFrom shapes inbound (download) datagrams.
func (c *shapedPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if n > 0 {
		waitBytes(c.down, n)
	}
	applyDelay(c.latency, c.jitter)
	return n, addr, err
}

// WriteTo shapes outbound (upload) datagrams and drops them with the
// configured loss probability. A dropped datagram reports success to the
// caller — mirroring how a lossy link silently discards packets.
func (c *shapedPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if c.loss > 0 && rand.Float64() < c.loss {
		applyDelay(c.latency, c.jitter)
		return len(b), nil
	}
	waitBytes(c.up, len(b))
	applyDelay(c.latency, c.jitter)
	return c.PacketConn.WriteTo(b, addr)
}
