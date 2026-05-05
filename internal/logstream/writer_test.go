package logstream

import (
	"testing"
	"time"

	"github.com/clambhook/clambhook/internal/events"
)

func TestWriterPublishesOneEventPerCompleteLine(t *testing.T) {
	bus := events.NewBus(events.Config{
		SubBufferSize: 8,
		RingCapacity:  8,
		MeterInterval: time.Hour,
	})
	defer bus.Close()

	sub := bus.Subscribe(events.Filter{Types: []string{"log.*"}})
	defer sub.Unsubscribe()

	w := NewWriter(bus)
	n, err := w.Write([]byte("first line\nsecond line\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("first line\nsecond line\n") {
		t.Fatalf("n = %d, want %d", n, len("first line\nsecond line\n"))
	}

	first := readLogEvent(t, sub)
	second := readLogEvent(t, sub)
	if first != "first line" || second != "second line" {
		t.Fatalf("lines = %q, %q; want first line, second line", first, second)
	}
}

func TestWriterBuffersPartialLineUntilNewline(t *testing.T) {
	bus := events.NewBus(events.Config{
		SubBufferSize: 8,
		RingCapacity:  8,
		MeterInterval: time.Hour,
	})
	defer bus.Close()

	sub := bus.Subscribe(events.Filter{Types: []string{"log.*"}})
	defer sub.Unsubscribe()

	w := NewWriter(bus)
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatalf("partial Write: %v", err)
	}
	assertNoLogEvent(t, sub)

	if _, err := w.Write([]byte(" line\n")); err != nil {
		t.Fatalf("newline Write: %v", err)
	}
	if got := readLogEvent(t, sub); got != "partial line" {
		t.Fatalf("line = %q, want partial line", got)
	}
}

func TestWriterWithNilBusAcceptsWrites(t *testing.T) {
	w := NewWriter(nil)
	n, err := w.Write([]byte("line\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("line\n") {
		t.Fatalf("n = %d, want %d", n, len("line\n"))
	}
}

func readLogEvent(t *testing.T, sub *events.Subscription) string {
	t.Helper()
	select {
	case ev := <-sub.Ch():
		if ev.Type != events.TypeLogLine {
			t.Fatalf("event type = %q, want %q", ev.Type, events.TypeLogLine)
		}
		data, ok := ev.Data.(events.LogLineData)
		if !ok {
			t.Fatalf("event data = %T, want events.LogLineData", ev.Data)
		}
		return data.Line
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for log event")
	}
	return ""
}

func assertNoLogEvent(t *testing.T, sub *events.Subscription) {
	t.Helper()
	select {
	case ev := <-sub.Ch():
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(25 * time.Millisecond):
	}
}
