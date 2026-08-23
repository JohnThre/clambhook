package developer

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/JohnThre/clambhook/internal/config"
	"github.com/JohnThre/clambhook/internal/listener"
)

func TestTransactionSetTimings(t *testing.T) {
	mgr, err := NewManager(config.DeveloperConfig{Enabled: true, CaptureLimit: 10})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := mgr.Begin(context.Background(), listener.HTTPCaptureMeta{Scheme: "http", Target: "example.com:80"}, req)
	if tx == nil {
		t.Fatal("nil inspection")
	}
	st, ok := tx.(listener.HTTPInspectionTimings)
	if !ok {
		t.Fatal("transaction does not implement listener.HTTPInspectionTimings")
	}
	st.SetTimings(listener.HTTPTimings{
		Connect: 5 * time.Millisecond,
		SSL:     10 * time.Millisecond,
		Send:    1 * time.Millisecond,
		Wait:    50 * time.Millisecond,
		Receive: 20 * time.Millisecond,
	})
	tx.Finish(nil, nil)

	entries := mgr.List(0)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	tm := entries[0].Timings
	if tm == nil {
		t.Fatal("nil timings")
	}
	if tm.Connect != 5 || tm.SSL != 10 || tm.Send != 1 || tm.Wait != 50 || tm.Receive != 20 {
		t.Fatalf("timings = %+v, want {5 10 1 50 20}", tm)
	}
}

func TestTransactionSetTimingsNilSafe(t *testing.T) {
	var tx *transaction
	tx.SetTimings(listener.HTTPTimings{Connect: time.Second}) // must not panic
}

func TestHARTimingsPopulatedFromEntry(t *testing.T) {
	entry := Entry{Timings: &Timings{Connect: 5, SSL: 10, Send: 1, Wait: 50, Receive: 20}}
	doc := harDocument([]Entry{entry})
	rows := doc["log"].(map[string]any)["entries"].([]map[string]any)
	timings := rows[0]["timings"].(map[string]any)
	if timings["blocked"] != -1 || timings["dns"] != -1 {
		t.Fatalf("blocked/dns = %v/%v, want -1/-1 (not applicable)", timings["blocked"], timings["dns"])
	}
	for k, want := range map[string]float64{"connect": 5, "ssl": 10, "send": 1, "wait": 50, "receive": 20} {
		if got, ok := timings[k].(float64); !ok || got != want {
			t.Fatalf("timings[%s] = %v, want %v", k, timings[k], want)
		}
	}
}

func TestHARTimingsFallbackWhenAbsent(t *testing.T) {
	started := time.Now()
	entry := Entry{StartedAt: started, FinishedAt: started.Add(120 * time.Millisecond)}
	doc := harDocument([]Entry{entry})
	rows := doc["log"].(map[string]any)["entries"].([]map[string]any)
	timings := rows[0]["timings"].(map[string]any)
	if timings["connect"] != -1 || timings["ssl"] != -1 {
		t.Fatalf("absent timings connect/ssl = %v/%v, want -1/-1", timings["connect"], timings["ssl"])
	}
	if got, ok := timings["wait"].(float64); !ok || got <= 0 {
		t.Fatalf("fallback wait = %v, want durationMs > 0", timings["wait"])
	}
	if timings["send"] != 0 || timings["receive"] != 0 {
		t.Fatalf("fallback send/receive = %v/%v, want 0/0", timings["send"], timings["receive"])
	}
}
