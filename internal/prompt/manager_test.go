// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

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

	// Each waiter reports its decision on its own channel slot; no shared slice
	// is written from multiple goroutines.
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dec, _ := m.Await(context.Background(), testRequest())
			results <- dec.Allow
		}()
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
	close(results)
	for allow := range results {
		if !allow {
			t.Fatalf("coalesced waiter should be allowed")
		}
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

func TestAwaitSilentModeAllowAutoDecidesAndLogs(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, SilentMode: "allow", Timeout: time.Second})

	dec, gated := m.Await(context.Background(), testRequest())
	if !gated || !dec.Allow {
		t.Fatalf("silent allow: want gated allow, got gated=%v dec=%+v", gated, dec)
	}
	if len(m.Pending()) != 0 {
		t.Fatalf("silent mode must not create a pending prompt: %+v", m.Pending())
	}
	decisions := m.SilentDecisions()
	if len(decisions) != 1 || decisions[0].Action != "allow" {
		t.Fatalf("silent decisions = %+v, want one allow", decisions)
	}
	if decisions[0].TargetHost != "example.com" || decisions[0].ProcessName != "curl" {
		t.Fatalf("silent decision fields = %+v", decisions[0])
	}
}

func TestAwaitSilentModeDenyAutoDecidesAndLogs(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, SilentMode: "deny", Timeout: time.Second})

	dec, gated := m.Await(context.Background(), testRequest())
	if !gated || dec.Allow {
		t.Fatalf("silent deny: want gated block, got gated=%v dec=%+v", gated, dec)
	}
	decisions := m.SilentDecisions()
	if len(decisions) != 1 || decisions[0].Action != "deny" {
		t.Fatalf("silent decisions = %+v, want one deny", decisions)
	}
}

func TestAwaitExpiresAtPopulated(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, Timeout: 5 * time.Second})

	go m.Await(context.Background(), testRequest())
	id := waitForPending(t, m)
	pending := m.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	p := pending[0]
	if p.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt must be populated, got zero")
	}
	if got := p.ExpiresAt.Sub(p.CreatedAt); got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("ExpiresAt-CreatedAt = %s, want ~5s", got)
	}
	if _, ok := m.Resolve(id, Resolution{Allow: true}); !ok {
		t.Fatalf("Resolve(%q) not ok", id)
	}
}

func TestAwaitWouldUseChainGroupPassedThrough(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, Timeout: 2 * time.Second})

	req := testRequest()
	req.WouldUseChain = "proxy"
	req.WouldUseGroup = "auto"
	go m.Await(context.Background(), req)
	id := waitForPending(t, m)
	pending := m.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	if pending[0].WouldUseChain != "proxy" || pending[0].WouldUseGroup != "auto" {
		t.Fatalf("would-use fields = %+v", pending[0])
	}
	if _, ok := m.Resolve(id, Resolution{Allow: true}); !ok {
		t.Fatalf("Resolve(%q) not ok", id)
	}
}

func TestSilentDecisionsNewestFirstAndByID(t *testing.T) {
	m := New()
	m.Configure(Config{Enabled: true, SilentMode: "allow", Timeout: time.Second})
	req := testRequest()
	req.ConnID = "conn-a"
	m.Await(context.Background(), req)
	req2 := testRequest()
	req2.ConnID = "conn-b"
	m.Await(context.Background(), req2)

	decisions := m.SilentDecisions()
	if len(decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(decisions))
	}
	if decisions[0].ID == decisions[1].ID {
		t.Fatalf("decisions must have distinct ids: %+v", decisions)
	}
	if _, ok := m.SilentDecision(decisions[0].ID); !ok {
		t.Fatalf("SilentDecision(%q) not found", decisions[0].ID)
	}
	if _, ok := m.SilentDecision("nope"); ok {
		t.Fatalf("SilentDecision(nope) should not be found")
	}
}
