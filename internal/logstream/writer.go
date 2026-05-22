// Package logstream mirrors standard logger output into the daemon event bus.
package logstream

import (
	"bytes"
	"sync"

	"github.com/JohnThre/clambhook/internal/events"
)

// Writer is an io.Writer that publishes newline-delimited log lines to the
// event bus. It is safe for concurrent use by the standard logger.
type Writer struct {
	bus *events.Bus
	mu  sync.Mutex
	buf bytes.Buffer
}

// NewWriter returns a log event writer. A nil bus is accepted and turns writes
// into no-ops so callers can wire it without branching.
func NewWriter(bus *events.Bus) *Writer {
	return &Writer{bus: bus}
}

// Write buffers partial lines and publishes each completed line as log.line.
// It always reports the full input length unless bytes.Buffer itself fails.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written := len(p)
	for len(p) > 0 {
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			if _, err := w.buf.Write(p[:i]); err != nil {
				return 0, err
			}
			w.publish(w.buf.String())
			w.buf.Reset()
			p = p[i+1:]
			continue
		}
		if _, err := w.buf.Write(p); err != nil {
			return 0, err
		}
		break
	}
	return written, nil
}

func (w *Writer) publish(line string) {
	if w.bus == nil {
		return
	}
	w.bus.PublishListener(events.TypeLogLine, events.LogLineData{Line: line})
}
