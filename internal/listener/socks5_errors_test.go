package listener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestReplyCodeForDialErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want byte
	}{
		{"nil", nil, repSuccess},
		{"deadline exceeded", context.DeadlineExceeded, repTTLExpired},
		{"wrapped deadline", fmt.Errorf("chain %q: %w", "x", context.DeadlineExceeded), repTTLExpired},
		{"dns error", &net.DNSError{Err: "no such host", Name: "nowhere.invalid"}, repHostUnreach},
		{"connection refused", syscall.ECONNREFUSED, repConnRefused},
		{"wrapped ECONNREFUSED", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), repConnRefused},
		{"network unreachable", syscall.ENETUNREACH, repNetworkUnreach},
		{"host unreachable", syscall.EHOSTUNREACH, repHostUnreach},
		{"op timeout", &net.OpError{Op: "dial", Err: timeoutError{}}, repTTLExpired},
		{"generic error", errors.New("some protocol-layer failure"), repGeneralFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replyCodeForDialErr(tt.err); got != tt.want {
				t.Errorf("got %#x, want %#x", got, tt.want)
			}
		})
	}
}

// timeoutError stands in for a net.OpError underlying error whose Timeout()
// returns true. Matches the interface net.OpError.Timeout() uses.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestSOCKSv5ConnectDialError verifies the listener actually sends the right
// reply code when the chain dial fails. Pairs replyCodeForDialErr with the
// handleConnect path so the two don't drift.
func TestSOCKSv5ConnectDialError(t *testing.T) {
	failingDial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, syscall.ECONNREFUSED
	}
	_, addr := newTestListener(t, nil, failingDial)

	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := readFull(client, got); err != nil {
		t.Fatal(err)
	}

	req := append([]byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1}, 0x00, 0x50)
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := readFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != repConnRefused {
		t.Errorf("got rep=%#x, want %#x (conn refused)", reply[1], repConnRefused)
	}
}

// readFull is a thin wrapper to keep the test body readable.
func readFull(r net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
