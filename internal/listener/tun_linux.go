//go:build linux

package listener

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/JohnThre/clambhook/internal/chain"
	"golang.zx2c4.com/wireguard/tun"
)

// TUN is a Linux device-wide ingress listener backed by a kernel TUN device
// and a userspace gVisor TCP/IP stack.
type TUN struct {
	opts    TUNOptions
	ch      *chain.Chain
	planner RoutePlanner

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	dev      tun.Device
	stack    *PacketStack
	routeMgr *linuxRouteManager
	wg       sync.WaitGroup
}

// NewTUN constructs a Linux TUN listener. Start owns privileged setup.
func NewTUN(opts TUNOptions, ch *chain.Chain) Listener {
	return &TUN{opts: opts, ch: ch}
}

func NewTUNWithPlanner(opts TUNOptions, ch *chain.Chain, planner RoutePlanner) Listener {
	return &TUN{opts: opts, ch: ch, planner: planner}
}

func TUNSupported() bool { return true }

func (t *TUN) Protocol() string { return "tun" }

func (t *TUN) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dev != nil {
		if name, err := t.dev.Name(); err == nil && name != "" {
			return name
		}
	}
	return t.opts.name()
}

func (t *TUN) ActiveConns() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stack == nil {
		return 0
	}
	return t.stack.ActiveConns()
}

func (t *TUN) Start(parent context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.dev != nil {
		return errors.New("tun: already started")
	}

	mtu := t.opts.mtu()
	dev, err := tun.CreateTUN(t.opts.name(), mtu)
	if err != nil {
		return fmt.Errorf("tun create %s: %w", t.opts.name(), err)
	}
	name, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("tun name: %w", err)
	}

	routeMgr := newLinuxRouteManager(name, mtu, t.opts, t.ch)
	if err := routeMgr.Setup(parent); err != nil {
		if t.opts.DNSProxy != nil {
			_ = t.opts.DNSProxy.Close()
		}
		_ = routeMgr.Cleanup(context.Background())
		_ = dev.Close()
		return fmt.Errorf("tun route setup: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	stack := NewPacketStack(t.opts, t.ch, t.planner, tunPacketWriter{dev: dev})
	if err := stack.Start(ctx); err != nil {
		cancel()
		if t.opts.DNSProxy != nil {
			_ = t.opts.DNSProxy.Close()
		}
		_ = routeMgr.Cleanup(context.Background())
		_ = dev.Close()
		return err
	}

	t.ctx = ctx
	t.cancel = cancel
	t.dev = dev
	t.stack = stack
	t.routeMgr = routeMgr

	t.wg.Add(1)
	go t.tunToStackLoop(ctx, dev, stack, mtu)

	log.Printf("tun listener started on %s (mtu=%d chain=%q)", name, mtu, t.opts.ChainName)
	return nil
}

func (t *TUN) Stop() error {
	t.mu.Lock()
	if t.dev == nil {
		dnsProxy := t.opts.DNSProxy
		t.mu.Unlock()
		if dnsProxy != nil {
			return dnsProxy.Close()
		}
		return nil
	}
	cancel := t.cancel
	dev := t.dev
	stack := t.stack
	routeMgr := t.routeMgr
	t.ctx = nil
	t.cancel = nil
	t.dev = nil
	t.stack = nil
	t.routeMgr = nil
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	var errs []error
	if routeMgr != nil {
		if err := routeMgr.Cleanup(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	if stack != nil {
		if err := stack.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	if dev != nil {
		if err := dev.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}

	done := make(chan struct{})
	go func() { t.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		log.Printf("tun: stop grace period expired; abandoning in-flight handlers")
	}

	return errors.Join(errs...)
}

func (t *TUN) tunToStackLoop(ctx context.Context, dev tun.Device, stack *PacketStack, mtu int) {
	defer t.wg.Done()

	batchSize := dev.BatchSize()
	if batchSize < 1 {
		batchSize = 1
	}
	bufs := make([][]byte, batchSize)
	sizes := make([]int, batchSize)
	for i := range bufs {
		bufs[i] = make([]byte, mtu+128)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := dev.Read(bufs, sizes, 0)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, os.ErrClosed) {
				return
			}
			log.Printf("tun: read: %v", err)
			return
		}
		for i := 0; i < n; i++ {
			if err := stack.InjectPacket(bufs[i][:sizes[i]]); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("tun: inject: %v", err)
				return
			}
		}
	}
}

type tunPacketWriter struct {
	dev tun.Device
}

func (w tunPacketWriter) WritePacket(pkt []byte) error {
	_, err := w.dev.Write([][]byte{pkt}, 0)
	return err
}

var _ Listener = (*TUN)(nil)
