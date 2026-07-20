package openvpn

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"testing"

	"golang.zx2c4.com/wireguard/tun"
)

// raceFakeTun is a no-op tun.Device: writeToTUN only calls Write, so the
// rest of the interface returns harmless stubs. It lets the race test
// publish a TUN device without standing up a full gVisor netstack.
type raceFakeTun struct {
	events chan tun.Event
}

func newRaceFakeTun() *raceFakeTun { return &raceFakeTun{events: make(chan tun.Event)} }

func (t *raceFakeTun) File() *os.File { return nil }

func (t *raceFakeTun) Read([][]byte, []int, int) (int, error) { return 0, io.EOF }

func (t *raceFakeTun) Write(bufs [][]byte, _ int) (int, error) { return len(bufs), nil }

func (t *raceFakeTun) MTU() (int, error) { return 1500, nil }

func (t *raceFakeTun) Name() (string, error) { return "race0", nil }

func (t *raceFakeTun) Events() <-chan tun.Event { return t.events }

func (t *raceFakeTun) BatchSize() int { return 1 }

func (t *raceFakeTun) Close() error { return nil }

// TestInstanceDataPlanePublicationRaceClean drives P_DATA_V2 datagrams
// into udpReadLoop at the same time the session state (data channel + TUN)
// is being published, reproducing the exact window between the UDP reader
// starting and the handshake/startNetstack completing. Run under `-race`
// it fails if any reader touches i.data or i.tunDev without the instance
// mutex. The read path here is 100% production code (udpReadLoop →
// dataChannelRef → dc.open → writeToTUN → device); only the publisher is
// the test, and it hands off through the same mutex the handshake uses.
func TestInstanceDataPlanePublicationRaceClean(t *testing.T) {
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer srv.Close()

	udp, err := net.DialUDP("udp", nil, srv.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	inst := &instance{
		udp:    udp,
		mtu:    1500,
		ctx:    bgCtx,
		cancel: cancel,
	}
	inst.wg.Add(1)
	go inst.udpReadLoop()

	// Pre-seal a monotonic stream of decryptable data packets from a
	// channel that shares keys with the receivers we publish below, so
	// dc.open succeeds and writeToTUN (which reads i.tunDev) is reached.
	km := makeKeys()
	sender := newDataChannel("CHACHA20-POLY1305", 0, km)
	sender.setPeerID(42)
	const iterations = 1000
	packets := make([][]byte, iterations)
	for j := range packets {
		pkt, err := sender.seal([]byte("publication-window-packet"))
		if err != nil {
			t.Fatalf("seal packet %d: %v", j, err)
		}
		packets[j] = pkt
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Publisher: emulate the handshake + startNetstack hand-off, swapping
	// in a fresh data channel and TUN device on every iteration through the
	// instance mutex to keep the publication window open for the reader.
	go func() {
		defer wg.Done()
		for j := 0; j < iterations; j++ {
			dc := newDataChannel("CHACHA20-POLY1305", 0, km)
			dc.setPeerID(42)
			dev := newRaceFakeTun()

			inst.mu.Lock()
			inst.data = dc
			inst.tunDev = dev
			inst.mu.Unlock()
		}
	}()

	// Injector: deliver the sealed packets so udpReadLoop reads i.data and,
	// on a successful open, i.tunDev via writeToTUN — all while the
	// publisher is mutating those very fields.
	go func() {
		defer wg.Done()
		local := udp.LocalAddr().(*net.UDPAddr)
		for j := 0; j < iterations; j++ {
			if _, err := srv.WriteToUDP(packets[j], local); err != nil {
				return
			}
		}
	}()

	wg.Wait()
	cancel()
	_ = udp.Close()
	inst.wg.Wait()
}
