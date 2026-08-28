// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package developer

import (
	"strconv"
	"testing"
)

func TestEntryFilterEmptyMatchesAll(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	got := s.Filter(EntryFilter{}, 0)
	if len(got) != 5 {
		t.Fatalf("empty filter returned %d, want 5", len(got))
	}
	// newest-first: the last-added entry (dev-5) is first.
	if got[0].ID != "dev-5" {
		t.Fatalf("first = %q, want dev-5 (newest first)", got[0].ID)
	}
}

func TestEntryFilterMethods(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	got := s.Filter(EntryFilter{Methods: []string{"post", "PUT"}}, 0)
	if len(got) != 2 {
		t.Fatalf("methods post|put = %d, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Method != "POST" && e.Method != "PUT" {
			t.Fatalf("unexpected method %q", e.Method)
		}
	}
}

func TestEntryFilterStatusRange(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	// 4xx + 5xx (dev-4=500, dev-5=404)
	if got := s.Filter(EntryFilter{StatusMin: 400, StatusMax: 599}, 0); len(got) != 2 {
		t.Fatalf("status 400-599 = %d, want 2", len(got))
	}
	// only 5xx (dev-4)
	if got := s.Filter(EntryFilter{StatusMin: 500}, 0); len(got) != 1 || got[0].ID != "dev-4" {
		t.Fatalf("status >=500 = %+v, want dev-4", got)
	}
	// only 2xx (dev-1, dev-2; newest-first dev-2)
	got := s.Filter(EntryFilter{StatusMax: 299}, 0)
	if len(got) != 2 {
		t.Fatalf("status <=299 = %d, want 2", len(got))
	}
	if got[0].ID != "dev-2" {
		t.Fatalf("status <=299 newest = %q, want dev-2", got[0].ID)
	}
}

func TestEntryFilterHostSchemeContentType(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	if got := s.Filter(EntryFilter{Host: "api."}, 0); len(got) != 2 {
		t.Fatalf("host api. = %d, want 2", len(got))
	}
	if got := s.Filter(EntryFilter{Scheme: "https"}, 0); len(got) != 2 {
		t.Fatalf("scheme https = %d, want 2", len(got))
	}
	if got := s.Filter(EntryFilter{ContentType: "json"}, 0); len(got) != 1 {
		t.Fatalf("content_type json = %d, want 1: %+v", len(got), got)
	}
}

func TestEntryFilterErrorOnly(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	got := s.Filter(EntryFilter{ErrorOnly: true}, 0)
	if len(got) != 1 || got[0].ID != "dev-3" {
		t.Fatalf("error_only = %+v, want dev-3", got)
	}
}

func TestEntryFilterQueryMatchesMetadataAndBody(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	// URL substring
	if got := s.Filter(EntryFilter{Query: "/v1/users"}, 0); len(got) != 1 || got[0].ID != "dev-1" {
		t.Fatalf("query /v1/users = %+v, want dev-1", got)
	}
	// header name + value
	if got := s.Filter(EntryFilter{Query: "x-trace:abc"}, 0); len(got) != 1 || got[0].ID != "dev-2" {
		t.Fatalf("query header = %+v, want dev-2", got)
	}
	// response body preview
	if got := s.Filter(EntryFilter{Query: "hello-json"}, 0); len(got) != 1 || got[0].ID != "dev-2" {
		t.Fatalf("query body = %+v, want dev-2", got)
	}
	// status as text
	if got := s.Filter(EntryFilter{Query: strconv.Itoa(500)}, 0); len(got) != 1 || got[0].ID != "dev-4" {
		t.Fatalf("query status 500 = %+v, want dev-4", got)
	}
	// error text
	if got := s.Filter(EntryFilter{Query: "dial timeout"}, 0); len(got) != 1 || got[0].ID != "dev-3" {
		t.Fatalf("query error = %+v, want dev-3", got)
	}
}

func TestEntryFilterQueryRedactedValueNotMatched(t *testing.T) {
	s := NewStore(10)
	s.Add(Entry{
		ID:     "dev-r",
		Method: "GET",
		URL:    "https://api.example.com/secret",
		Host:   "api.example.com",
		Status: 200,
		Request: Message{Headers: []Header{
			{Name: "Authorization", Value: redactedValue, Redacted: true},
		}},
	})
	// The real secret value must not match (it was redacted to "[redacted]").
	if got := s.Filter(EntryFilter{Query: "supersecret"}, 0); len(got) != 0 {
		t.Fatalf("redacted value leaked via query: %+v", got)
	}
	// The header name still matches.
	if got := s.Filter(EntryFilter{Query: "authorization"}, 0); len(got) != 1 {
		t.Fatalf("header name should match: %+v", got)
	}
}

func TestStoreFilterLimitCapAndZero(t *testing.T) {
	s := NewStore(10)
	addFilteredEntries(t, s)
	// https matches dev-1? no — dev-1 is http. https entries: dev-2, dev-4.
	if got := s.Filter(EntryFilter{Scheme: "https"}, 1); len(got) != 1 {
		t.Fatalf("limit 1 = %d, want 1", len(got))
	}
	if got := s.Filter(EntryFilter{Scheme: "https"}, 1); got[0].ID != "dev-4" {
		t.Fatalf("capped filter not newest-first: %q", got[0].ID)
	}
	// limit 0 = all matches
	if got := s.Filter(EntryFilter{Scheme: "https"}, 0); len(got) != 2 {
		t.Fatalf("limit 0 = %d, want 2", len(got))
	}
}

func TestStoreFilterNilStore(t *testing.T) {
	var s *Store
	if got := s.Filter(EntryFilter{}, 0); len(got) != 0 {
		t.Fatalf("nil store filter = %d, want 0", len(got))
	}
}

// addFilteredEntries seeds a store with a small, varied set of captures.
//
//	dev-1: GET     http  200  /v1/users     (text/plain)
//	dev-2: PUT     https 201  /v1/items     (application/json, X-Trace: abc)
//	dev-3: POST    http  0    /v1/timeout   (error: dial timeout)
//	dev-4: DELETE  https 500  /v1/items/9   (text/html)
//	dev-5: GET     http  404  /v1/missing   (text/plain)
func addFilteredEntries(t *testing.T, s *Store) {
	t.Helper()
	entries := []Entry{
		{
			ID: "dev-1", Method: "GET", URL: "http://example.com/v1/users",
			Scheme: "http", Host: "example.com", Status: 200,
			Request:  Message{Headers: []Header{{Name: "Accept", Value: "text/plain"}}},
			Response: Message{Body: Body{MimeType: "text/plain", Preview: "hello-text"}},
		},
		{
			ID: "dev-2", Method: "PUT", URL: "https://api.example.com/v1/items",
			Scheme: "https", Host: "api.example.com", Status: 201,
			Request:  Message{Headers: []Header{{Name: "X-Trace", Value: "abc"}}},
			Response: Message{Body: Body{MimeType: "application/json", Preview: "hello-json"}},
		},
		{
			ID: "dev-3", Method: "POST", URL: "http://example.com/v1/timeout",
			Scheme: "http", Host: "example.com", Status: 0,
			Error: "dial timeout",
		},
		{
			ID: "dev-4", Method: "DELETE", URL: "https://api.example.com/v1/items/9",
			Scheme: "https", Host: "api.example.com", Status: 500,
			Response: Message{Body: Body{MimeType: "text/html"}},
		},
		{
			ID: "dev-5", Method: "GET", URL: "http://example.com/v1/missing",
			Scheme: "http", Host: "example.com", Status: 404,
			Response: Message{Body: Body{MimeType: "text/plain"}},
		},
	}
	for _, e := range entries {
		s.Add(e)
	}
}
