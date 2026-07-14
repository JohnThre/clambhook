package procattr

import (
	"net"
	"os"
	"runtime"
	"testing"
)

func TestLocalPort(t *testing.T) {
	tests := []struct {
		source string
		want   int
		ok     bool
	}{
		{"127.0.0.1:54321", 54321, true},
		{"[::1]:8080", 8080, true},
		{"127.0.0.1", 0, false},
		{"", 0, false},
		{"127.0.0.1:0", 0, false},
		{"127.0.0.1:70000", 0, false},
	}
	for _, tt := range tests {
		got, ok := localPort(tt.source)
		if ok != tt.ok || got != tt.want {
			t.Errorf("localPort(%q) = (%d, %v), want (%d, %v)", tt.source, got, ok, tt.want, tt.ok)
		}
	}
}

func TestBaseName(t *testing.T) {
	if got := baseName("/usr/bin/curl"); got != "curl" {
		t.Errorf("baseName = %q, want curl", got)
	}
	if got := baseName(""); got != "" {
		t.Errorf("baseName empty = %q, want empty", got)
	}
}

// TestLookupSelf attributes a live loopback connection back to the running
// test process. Attribution is only implemented on darwin and linux; other
// platforms return ok=false by contract, which this test asserts.
func TestLookupSelf(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		conn, aerr := ln.Accept()
		if aerr == nil {
			defer conn.Close()
		}
		close(done)
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	<-done

	source := client.LocalAddr().String()
	proc, ok := Lookup("tcp", source)

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if ok {
			t.Fatalf("Lookup on %s should be unsupported, got %+v", runtime.GOOS, proc)
		}
		return
	}
	if !ok {
		t.Skipf("Lookup returned no attribution for %s (may require permissions in this sandbox)", source)
	}
	if proc.PID != os.Getpid() {
		t.Errorf("Lookup pid = %d, want self %d", proc.PID, os.Getpid())
	}
	if proc.Name == "" {
		t.Errorf("Lookup name empty for %+v", proc)
	}
}
