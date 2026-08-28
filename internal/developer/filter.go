package developer

import (
	"strconv"
	"strings"
)

// EntryFilter narrows the in-memory capture ring. Every field is optional; an
// empty EntryFilter matches every entry (back-compatible with the pre-filter
// "list all" behavior). Methods is a case-insensitive allowlist; Host and
// ContentType are case-insensitive substrings; Scheme is an exact
// case-insensitive match; StatusMin/StatusMax are inclusive bounds (0 means
// unbounded); ErrorOnly keeps entries with a non-empty Error; Query is a
// case-insensitive substring over the composite search blob (method, url,
// host, scheme, chain, profile, status, error, both bodies' MIME types,
// header names/values, and both body previews). Redacted header values are
// stored as "[redacted]", so a query for a real secret value never matches.
type EntryFilter struct {
	Methods     []string
	StatusMin   int
	StatusMax   int
	Host        string
	Scheme      string
	ContentType string
	Query       string
	ErrorOnly   bool
}

// Filter returns matching entries newest-first, capped at limit. limit <= 0
// returns every match (bounded by the ring size). It performs no allocation
// beyond the result slice and a query blob computed only when Query is set.
func (s *Store) Filter(f EntryFilter, limit int) []Entry {
	if s == nil {
		return []Entry{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.entries))
	query := strings.ToLower(strings.TrimSpace(f.Query))
	needBlob := query != ""
	for _, e := range s.entries { // stored newest-first
		if needBlob && !strings.Contains(entrySearchBlob(e), query) {
			continue
		}
		if !f.matches(e) {
			continue
		}
		out = append(out, cloneEntry(e))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (f EntryFilter) matches(e Entry) bool {
	if len(f.Methods) > 0 {
		ok := false
		for _, m := range f.Methods {
			if strings.EqualFold(strings.TrimSpace(m), e.Method) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// A status bound filters by *response* status. An entry with status 0
	// (no response yet, e.g. a dial error) has no status to compare, so it is
	// excluded whenever any status filter is active; use error_only to surface
	// those.
	if f.StatusMin > 0 || f.StatusMax > 0 {
		if e.Status == 0 {
			return false
		}
		if f.StatusMin > 0 && e.Status < f.StatusMin {
			return false
		}
		if f.StatusMax > 0 && e.Status > f.StatusMax {
			return false
		}
	}
	if f.Host != "" && !strings.Contains(strings.ToLower(e.Host), strings.ToLower(f.Host)) {
		return false
	}
	if f.Scheme != "" && !strings.EqualFold(f.Scheme, e.Scheme) {
		return false
	}
	if f.ContentType != "" {
		ct := strings.ToLower(f.ContentType)
		if !strings.Contains(strings.ToLower(e.Request.Body.MimeType), ct) &&
			!strings.Contains(strings.ToLower(e.Response.Body.MimeType), ct) {
			return false
		}
	}
	if f.ErrorOnly && e.Error == "" {
		return false
	}
	return true
}

// entrySearchBlob returns the lowercased composite text a free-text query is
// matched against. It spans the metadata clients surface plus header names and
// values and body previews so a single search box finds a request by any
// visible token. Redacted header values appear as "[redacted]" only.
func entrySearchBlob(e Entry) string {
	var b strings.Builder
	b.Grow(256)
	b.WriteString(e.Method)
	b.WriteByte(' ')
	b.WriteString(e.URL)
	b.WriteByte(' ')
	b.WriteString(e.Host)
	b.WriteByte(' ')
	b.WriteString(e.Scheme)
	b.WriteByte(' ')
	b.WriteString(e.ChainName)
	b.WriteByte(' ')
	b.WriteString(e.Profile)
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(e.Status))
	b.WriteByte(' ')
	b.WriteString(e.Error)
	b.WriteByte(' ')
	b.WriteString(e.Request.Body.MimeType)
	b.WriteByte(' ')
	b.WriteString(e.Response.Body.MimeType)
	b.WriteByte(' ')
	for _, h := range e.Request.Headers {
		b.WriteString(h.Name)
		b.WriteByte(':')
		b.WriteString(h.Value)
		b.WriteByte(' ')
	}
	for _, h := range e.Response.Headers {
		b.WriteString(h.Name)
		b.WriteByte(':')
		b.WriteString(h.Value)
		b.WriteByte(' ')
	}
	b.WriteString(e.Request.Body.Preview)
	b.WriteByte(' ')
	b.WriteString(e.Response.Body.Preview)
	return strings.ToLower(b.String())
}
