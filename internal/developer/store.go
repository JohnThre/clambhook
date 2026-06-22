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
	Size           int64  `json:"size"`
	Preview        string `json:"preview,omitempty"`
	PreviewBase64  string `json:"preview_base64,omitempty"`
	PreviewBytes   int64  `json:"preview_bytes"`
	Truncated      bool   `json:"truncated"`
	TruncatedAfter int64  `json:"truncated_after"`
	MimeType       string `json:"mime_type,omitempty"`
	Encoding       string `json:"encoding,omitempty"`
}

// Message contains captured request or response data.
type Message struct {
	Headers []Header `json:"headers,omitempty"`
	Cookies []Cookie `json:"cookies,omitempty"`
	Body    Body     `json:"body"`
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
	return entry
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
