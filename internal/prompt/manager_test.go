package prompt

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/procattr"
)

func testRequest() Request {
	return Request{
		ConnID:  "conn-1",
		Profile: "default",
		Network: "tcp",
		Target:  "example.com:443",
		Host:    "example.com",
		Port:    "443",
		Process: procattr.Process{PID: 42, Name: "curl", Path: "/usr/bin/curl"},
	}
}

func TestAwaitDisabledIsNotGated(t *testing.T) {
	m := New()
	dec, gated := m.Await(context.Background(), testRequest())
	if gated {
		t.Fatalf("disabled manager must not gate, got gated=true dec=%+v", dec)
	}
}

func TestAwaitResolveAllow(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, Timeout: time.Second})

	type result struct {
		dec   Decision
		gated bool
	}
	res := make(chan result, 1)
	go func() {
		dec, gated := m.Await(context.Background(), testRequest())
		res <- result{dec, gated}
	}()

	id := waitForPending(t, m)
	if _, ok := m.Resolve(id, Resolution{Allow: true}); !ok {
		t.Fatalf("Resolve(%q) returned not ok", id)
	}
	select {
	case r := <-res:
		if !r.gated || !r.dec.Allow {
			t.Fatalf("want gated allow, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Await did not return after Resolve")
	}
	if len(m.Pending()) != 0 {
		t.Fatalf("pending not cleared after resolve: %+v", m.Pending())
	}
}

func TestAwaitTimeoutAppliesDefault(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, Timeout: 50 * time.Millisecond, DefaultAllow: false})

	start := time.Now()
	dec, gated := m.Await(context.Background(), testRequest())
	if !gated {
		t.Fatal("timed-out prompt must still be gated")
	}
	if dec.Allow {
		t.Fatal("timeout with DefaultAllow=false must block")
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("returned too early (%s); timeout not honored", elapsed)
	}
}

func TestAwaitCoalescesWaiters(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, Timeout: 2 * time.Second})

	var wg sync.WaitGroup
	allowed := make([]bool, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			dec, _ := m.Await(context.Background(), testRequest())
			allowed[idx] = dec.Allow
		}(i)
	}

	// Wait for both waiters to coalesce onto one pending prompt.
	id := ""
	deadline := time.After(2 * time.Second)
	for {
		pending := m.Pending()
		if len(pending) == 1 && pending[0].Waiters == 2 {
			id = pending[0].ID
			break
		}
		if len(pending) > 1 {
			t.Fatalf("waiters did not coalesce: %d pending", len(pending))
		}
		select {
		case <-deadline:
			t.Fatalf("waiters never coalesced (pending=%+v)", pending)
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}

	if _, ok := m.Resolve(id, Resolution{Allow: true}); !ok {
		t.Fatalf("Resolve(%q) not ok", id)
	}
	wg.Wait()
	if !allowed[0] || !allowed[1] {
		t.Fatalf("both coalesced waiters should be allowed, got %+v", allowed)
	}
}

func TestEventHookFires(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, Timeout: time.Second})

	var mu sync.Mutex
	kinds := map[string]bool{}
	m.SetEventHook(func(kind string, _ Pending, _ bool) {
		mu.Lock()
		kinds[kind] = true
		mu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		m.Await(context.Background(), testRequest())
		close(done)
	}()
	id := waitForPending(t, m)
	m.Resolve(id, Resolution{Allow: false})
	<-done

	mu.Lock()
	defer mu.Unlock()
	if !kinds[EventPending] || !kinds[EventResolved] {
		t.Fatalf("expected pending+resolved hooks, got %+v", kinds)
	}
}

func waitForPending(t *testing.T, m *Manager) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if p := m.Pending(); len(p) > 0 {
			return p[0].ID
		}
		select {
		case <-deadline:
			t.Fatal("no pending prompt appeared")
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}
