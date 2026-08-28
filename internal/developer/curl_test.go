// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package developer

import (
	"strings"
	"testing"
)

func TestEntryToCurlGETNoBody(t *testing.T) {
	got := entryToCurl(Entry{
		Method: "GET",
		URL:    "https://example.com/path?q=1",
		Request: Message{Headers: []Header{
			{Name: "Accept", Value: "application/json"},
			{Name: "Authorization", Value: redactedValue, Redacted: true},
			{Name: "Content-Length", Value: "0"},
			{Name: "Host", Value: "example.com"},
		}},
	})
	if !strings.HasPrefix(got, "curl ") {
		t.Fatalf("missing curl prefix: %q", got)
	}
	// GET must not add an explicit -X.
	if strings.Contains(got, " -X ") {
		t.Fatalf("GET should not emit -X: %q", got)
	}
	if !strings.Contains(got, "-H 'Accept: application/json'") {
		t.Fatalf("missing header: %q", got)
	}
	if strings.Contains(got, "-H 'Authorization") {
		t.Fatalf("redacted header leaked as -H arg: %q", got)
	}
	if strings.Contains(got, "Content-Length") || strings.Contains(got, " -H 'Host") {
		t.Fatalf("managed header not dropped: %q", got)
	}
	if !strings.Contains(got, " 'https://example.com/path?q=1'") {
		t.Fatalf("missing url: %q", got)
	}
	if !strings.Contains(got, "# redacted headers omitted: Authorization") {
		t.Fatalf("missing redaction comment: %q", got)
	}
	if strings.Contains(got, "--data-raw") {
		t.Fatalf("GET with empty body should not add --data-raw: %q", got)
	}
}

func TestEntryToCurlPostWithBody(t *testing.T) {
	got := entryToCurl(Entry{
		Method: "POST",
		URL:    "https://api.example.com/v1/items",
		Request: Message{
			Headers: []Header{{Name: "Content-Type", Value: "application/json"}},
			Body:    Body{Preview: `{"a":1}`, Truncated: false},
		},
	})
	if !strings.Contains(got, "curl -X 'POST'") {
		t.Fatalf("missing -X POST: %q", got)
	}
	if !strings.Contains(got, "--data-raw '{\"a\":1}'") {
		t.Fatalf("missing data-raw: %q", got)
	}
	if !strings.Contains(got, " 'https://api.example.com/v1/items'") {
		t.Fatalf("missing url: %q", got)
	}
}

func TestEntryToCurlTruncatedBodyWarns(t *testing.T) {
	got := entryToCurl(Entry{
		Method: "POST",
		URL:    "https://example.com/upload",
		Request: Message{
			Headers: []Header{{Name: "Content-Type", Value: "application/json"}},
			Body:    Body{Preview: `{"partia`, Truncated: true},
		},
	})
	if !strings.Contains(got, "# warning: captured request body was truncated") {
		t.Fatalf("missing truncation warning: %q", got)
	}
}

func TestEntryToCurlEscapesSingleQuotes(t *testing.T) {
	got := entryToCurl(Entry{
		Method: "GET",
		URL:    "https://example.com/p",
		Request: Message{Headers: []Header{
			{Name: "X-Quote", Value: "it's a test"},
		}},
	})
	if !strings.Contains(got, "-H 'X-Quote: it'\\''s a test'") {
		t.Fatalf("single quote not escaped: %q", got)
	}
}

func TestEntryToCurlDefaultsMethodToGET(t *testing.T) {
	got := entryToCurl(Entry{URL: "https://example.com"})
	if !strings.HasPrefix(got, "curl ") {
		t.Fatalf("missing curl prefix: %q", got)
	}
	if strings.Contains(got, " -X ") {
		t.Fatalf("default method should not emit -X: %q", got)
	}
}

func TestCurlToRepeatBasic(t *testing.T) {
	out, err := curlToRepeat(`curl https://api.example.com -H 'X: y' -d '{"a":1}'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Method != "POST" {
		t.Fatalf("method = %q, want POST (data implies POST)", out.Method)
	}
	if out.URL != "https://api.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
	if len(out.Headers) != 1 || out.Headers[0].Name != "X" || out.Headers[0].Value != "y" {
		t.Fatalf("headers = %+v", out.Headers)
	}
	if out.Body != `{"a":1}` {
		t.Fatalf("body = %q", out.Body)
	}
}

func TestCurlToRepeatExplicitMethodGETWithBody(t *testing.T) {
	out, err := curlToRepeat(`curl -X GET https://api.example.com -d 'q=1'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Explicit -X GET must win over the -d-implied POST.
	if out.Method != "GET" {
		t.Fatalf("method = %q, want GET", out.Method)
	}
	if out.Body != "q=1" {
		t.Fatalf("body = %q", out.Body)
	}
}

func TestCurlToRepeatMultipleDataConcatenated(t *testing.T) {
	out, err := curlToRepeat(`curl https://api.example.com -d 'a=1' -d 'b=2'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Body != "a=1&b=2" {
		t.Fatalf("body = %q, want a=1&b=2 (curl concatenates with &)", out.Body)
	}
}

func TestCurlToRepeatBenignFlagsAccepted(t *testing.T) {
	out, err := curlToRepeat(`curl --compressed -k -L -i https://api.example.com`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.URL != "https://api.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
}

func TestCurlToRepeatUserAgentAndReferer(t *testing.T) {
	out, err := curlToRepeat(`curl https://api.example.com -A 'MyAgent' -e 'https://ref.test'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	find := func(name string) string {
		for _, h := range out.Headers {
			if h.Name == name {
				return h.Value
			}
		}
		return ""
	}
	if find("User-Agent") != "MyAgent" {
		t.Fatalf("user-agent = %+v", out.Headers)
	}
	if find("Referer") != "https://ref.test" {
		t.Fatalf("referer = %+v", out.Headers)
	}
}

func TestCurlToRepeatUnknownFlagIgnored(t *testing.T) {
	out, err := curlToRepeat(`curl --weird-flag https://api.example.com -H 'X: y'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.URL != "https://api.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
	if len(out.Headers) != 1 {
		t.Fatalf("headers = %+v", out.Headers)
	}
}

func TestCurlToRepeatValueFlagConsumesValue(t *testing.T) {
	// -o /dev/null is a known value-taking flag; its value must NOT be read as the URL.
	out, err := curlToRepeat(`curl -o /dev/null https://api.example.com`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.URL != "https://api.example.com" {
		t.Fatalf("url = %q (value flag leaked into URL)", out.URL)
	}
}

func TestCurlToRepeatErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no url", `curl -H 'X: y'`},
		{"unterminated single quote", `curl -H 'X: y `},
		{"unterminated double quote", `curl -H "X: y`},
		{"atfile body", `curl https://api.example.com -d @body.txt`},
		{"-X missing value", `curl -X`},
		{"-H missing value", `curl https://api.example.com -H`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := curlToRepeat(tc.in); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.in)
			}
		})
	}
}

func TestCurlToRepeatDropsLeadingCurlCommand(t *testing.T) {
	out, err := curlToRepeat(`/usr/bin/curl https://api.example.com`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.URL != "https://api.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
}

func TestCurlToRepeatDoubleQuotesAndEscape(t *testing.T) {
	out, err := curlToRepeat(`curl "https://api.example.com" -H "X-Y: a\"b"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.URL != "https://api.example.com" {
		t.Fatalf("url = %q", out.URL)
	}
	if len(out.Headers) != 1 || out.Headers[0].Name != "X-Y" || out.Headers[0].Value != `a"b` {
		t.Fatalf("header = %+v", out.Headers)
	}
}

func TestCurlToRepeatRoundTripWithEntryToCurl(t *testing.T) {
	// A captured entry -> cURL -> parsed reproduces the method, url, headers, body.
	orig := Entry{
		Method: "POST",
		URL:    "https://api.example.com/v1/items",
		Request: Message{
			Headers: []Header{{Name: "Content-Type", Value: "application/json"}},
			Body:    Body{Preview: `{"a":1}`},
		},
	}
	cmd := entryToCurl(orig)
	parsed, err := curlToRepeat(cmd)
	if err != nil {
		t.Fatalf("round-trip parse error: %v\n%s", err, cmd)
	}
	if parsed.Method != "POST" || parsed.URL != orig.URL {
		t.Fatalf("round-trip method/url = %q %q, want POST %s", parsed.Method, parsed.URL, orig.URL)
	}
	if parsed.Body != orig.Request.Body.Preview {
		t.Fatalf("round-trip body = %q, want %q", parsed.Body, orig.Request.Body.Preview)
	}
	if len(parsed.Headers) != 1 || parsed.Headers[0].Name != "Content-Type" {
		t.Fatalf("round-trip headers = %+v", parsed.Headers)
	}
}
