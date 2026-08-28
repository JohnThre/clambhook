// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package developer

import (
	"sync"
	"time"
)

// Header is a captured HTTP header value.
type Header struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Redacted  bool   `json:"redacted,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Cookie is a captured HTTP cookie. Sensitive values may be redacted while
// retaining non-secret attributes useful for debugging.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Expires  string `json:"expires,omitempty"`
	MaxAge   int    `json:"max_age,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"http_only,omitempty"`
	SameSite string `json:"same_site,omitempty"`
}

// Body is a bounded body preview.
type Body struct {
	Size           int64       `json:"size"`
	Preview        string      `json:"preview,omitempty"`
	PreviewBase64  string      `json:"preview_base64,omitempty"`
	PreviewBytes   int64       `json:"preview_bytes"`
	Truncated      bool        `json:"truncated"`
	TruncatedAfter int64       `json:"truncated_after"`
	MimeType       string      `json:"mime_type,omitempty"`
	Encoding       string      `json:"encoding,omitempty"`
	Viewer         *BodyViewer `json:"viewer,omitempty"`
}

// Message contains captured request or response data.
type Message struct {
	Headers []Header `json:"headers,omitempty"`
	Cookies []Cookie `json:"cookies,omitempty"`
	Body    Body     `json:"body"`
}

// DecodedFrame is one decoded application-protocol message (a WebSocket frame,
// a gRPC message, or a GraphQL request/response section). It mirrors
// decode.Frame so the store has no dependency on the decode package.
type DecodedFrame struct {
	Direction string `json:"direction"`
	Opcode    string `json:"opcode,omitempty"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Decoded is an optional structured view of a captured transaction whose
// payload is a recognized application protocol. Clients render it in place of
// the raw body preview; when absent, clients fall back to the body preview.
type Decoded struct {
	Kind   string         `json:"kind"`
	Frames []DecodedFrame `json:"frames,omitempty"`
}

// Timings is the connect/SSL/send/wait/receive breakdown of a captured
// transaction in milliseconds. It mirrors the HAR 1.2 timings block. Zero or
// nil values indicate the phase did not apply (e.g. Map Local synthetic
// responses have no upstream dial, plain HTTP has no SSL). Wait is the
// server-processing / time-to-first-byte window; receive is the response body
// transfer window. Breakpoint and rewrite pauses are excluded (milestones are
// recorded around network I/O only).
type Timings struct {
	Connect float64 `json:"connect,omitempty"`
	SSL     float64 `json:"ssl,omitempty"`
	Send    float64 `json:"send,omitempty"`
	Wait    float64 `json:"wait,omitempty"`
	Receive float64 `json:"receive,omitempty"`
}

// BodyViewer is a daemon-side-computed rendering of a captured body so all four
// clients render one shared shape instead of each reimplementing pretty-print
// and hex. Pretty is the re-indented text (JSON/XML/form/HTML); Hex is an
// offset/hex/ascii dump. Both are derived from the bounded preview bytes only,
// so they reflect the same truncation as Preview. Kind is json|xml|form|html|text.
// When a body cannot be pretty-printed (binary, invalid, base64-only), Pretty is
// empty and clients fall back to the raw preview; Hex is still populated for
// binary bodies. A nil Viewer (e.g. for empty bodies) means no viewer applies.
type BodyViewer struct {
	Kind            string `json:"kind,omitempty"`
	Pretty          string `json:"pretty,omitempty"`
	PrettyTruncated bool   `json:"pretty_truncated,omitempty"`
	Hex             string `json:"hex,omitempty"`
}

// Entry is one captured HTTP transaction.
type Entry struct {
	ID         string    `json:"id"`
	ConnID     string    `json:"conn_id,omitempty"`
	Profile    string    `json:"profile,omitempty"`
	ClientAddr string    `json:"client_addr,omitempty"`
	ChainName  string    `json:"chain_name,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Scheme     string    `json:"scheme"`
	Host       string    `json:"host"`
	Status     int       `json:"status,omitempty"`
	Request    Message   `json:"request"`
	Response   Message   `json:"response"`
	Decoded    *Decoded  `json:"decoded,omitempty"`
	Timings    *Timings  `json:"timings,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Store keeps bounded in-memory captures, newest first.
type Store struct {
	mu      sync.RWMutex
	limit   int
	entries []Entry
}

func NewStore(limit int) *Store {
	if limit <= 0 {
		limit = 200
	}
	return &Store{limit: limit}
}

func (s *Store) Reconfigure(limit int) {
	if s == nil {
		return
	}
	if limit <= 0 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit = limit
	if len(s.entries) > s.limit {
		s.entries = append([]Entry(nil), s.entries[:s.limit]...)
	}
}

func (s *Store) Add(entry Entry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append([]Entry{cloneEntry(entry)}, s.entries...)
	if len(s.entries) > s.limit {
		s.entries = s.entries[:s.limit]
	}
}

func (s *Store) List(limit int) []Entry {
	if s == nil {
		return []Entry{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.entries)
	if limit > 0 && n > limit {
		n = limit
	}
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, cloneEntry(s.entries[i]))
	}
	return out
}

func (s *Store) Get(id string) (Entry, bool) {
	if s == nil {
		return Entry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.entries {
		if entry.ID == id {
			return cloneEntry(entry), true
		}
	}
	return Entry{}, false
}

func (s *Store) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}

func cloneEntry(entry Entry) Entry {
	entry.Request.Headers = cloneHeaderSlice(entry.Request.Headers)
	entry.Request.Cookies = cloneCookieSlice(entry.Request.Cookies)
	entry.Response.Headers = cloneHeaderSlice(entry.Response.Headers)
	entry.Response.Cookies = cloneCookieSlice(entry.Response.Cookies)
	entry.Request.Body.Viewer = cloneBodyViewer(entry.Request.Body.Viewer)
	entry.Response.Body.Viewer = cloneBodyViewer(entry.Response.Body.Viewer)
	entry.Decoded = cloneDecoded(entry.Decoded)
	entry.Timings = cloneTimings(entry.Timings)
	return entry
}

func cloneTimings(t *Timings) *Timings {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}

func cloneBodyViewer(v *BodyViewer) *BodyViewer {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func cloneDecoded(decoded *Decoded) *Decoded {
	if decoded == nil {
		return nil
	}
	out := &Decoded{Kind: decoded.Kind}
	if len(decoded.Frames) > 0 {
		out.Frames = make([]DecodedFrame, len(decoded.Frames))
		copy(out.Frames, decoded.Frames)
	}
	return out
}

func cloneHeaderSlice(headers []Header) []Header {
	if len(headers) == 0 {
		return nil
	}
	out := make([]Header, len(headers))
	copy(out, headers)
	return out
}

func cloneCookieSlice(cookies []Cookie) []Cookie {
	if len(cookies) == 0 {
		return nil
	}
	out := make([]Cookie, len(cookies))
	copy(out, cookies)
	return out
}
