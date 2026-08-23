package developer

import (
	"net/http"
	"net/url"
	"time"
)

func harDocument(entries []Entry) map[string]any {
	rows := make([]map[string]any, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		rows = append(rows, harEntry(entries[i]))
	}
	return map[string]any{
		"log": map[string]any{
			"version": "1.2",
			"creator": map[string]any{
				"name":    "clambhook",
				"version": "dev",
			},
			"entries": rows,
		},
	}
}

func harEntry(entry Entry) map[string]any {
	started := entry.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	durationMs := 0.0
	if !entry.FinishedAt.IsZero() && !entry.StartedAt.IsZero() {
		durationMs = float64(entry.FinishedAt.Sub(entry.StartedAt).Microseconds()) / 1000
	}
	timings := harTimings(entry, durationMs)
	return map[string]any{
		"startedDateTime": started.UTC().Format(time.RFC3339Nano),
		"time":            durationMs,
		"request": map[string]any{
			"method":      entry.Method,
			"url":         entry.URL,
			"httpVersion": "HTTP/1.1",
			"headers":     harHeaders(entry.Request.Headers),
			"queryString": harQueryString(entry.URL),
			"cookies":     harCookies(entry.Request.Cookies),
			"headersSize": -1,
			"bodySize":    entry.Request.Body.Size,
			"postData":    harPostData(entry.Request.Body),
		},
		"response": map[string]any{
			"status":      entry.Status,
			"statusText":  http.StatusText(entry.Status),
			"httpVersion": "HTTP/1.1",
			"headers":     harHeaders(entry.Response.Headers),
			"cookies":     harCookies(entry.Response.Cookies),
			"content":     harContent(entry.Response.Body),
			"redirectURL": "",
			"headersSize": -1,
			"bodySize":    entry.Response.Body.Size,
		},
		"cache":   map[string]any{},
		"timings": timings,
		"_clambhook": map[string]any{
			"id":                       entry.ID,
			"conn_id":                  entry.ConnID,
			"profile":                  entry.Profile,
			"chain_name":               entry.ChainName,
			"client_addr":              entry.ClientAddr,
			"scheme":                   entry.Scheme,
			"host":                     entry.Host,
			"error":                    entry.Error,
			"request_body_truncated":   entry.Request.Body.Truncated,
			"response_body_truncated":  entry.Response.Body.Truncated,
			"request_preview_bytes":    entry.Request.Body.PreviewBytes,
			"response_preview_bytes":   entry.Response.Body.PreviewBytes,
			"request_truncated_after":  entry.Request.Body.TruncatedAfter,
			"response_truncated_after": entry.Response.Body.TruncatedAfter,
		},
	}
}

// harTimings builds the HAR 1.2 timings block from a captured transaction's
// measured connect/SSL/send/wait/receive breakdown. blocked and dns are -1
// (not applicable — the daemon route planner already resolved the target and
// no client-side queueing is measured). When no timings were captured (Map
// Local synthetic responses, or captures from before timings existed) the
// connect/ssl phases are -1 and the whole duration falls into wait, preserving
// the pre-timings HAR shape.
func harTimings(entry Entry, durationMs float64) map[string]any {
	t := map[string]any{
		"blocked": -1,
		"dns":     -1,
	}
	if entry.Timings != nil {
		t["connect"] = entry.Timings.Connect
		t["ssl"] = entry.Timings.SSL
		t["send"] = entry.Timings.Send
		t["wait"] = entry.Timings.Wait
		t["receive"] = entry.Timings.Receive
	} else {
		t["connect"] = -1
		t["ssl"] = -1
		t["send"] = 0
		t["wait"] = durationMs
		t["receive"] = 0
	}
	return t
}

func harHeaders(headers []Header) []map[string]any {
	out := make([]map[string]any, 0, len(headers))
	for _, header := range headers {
		row := map[string]any{
			"name":  header.Name,
			"value": header.Value,
		}
		if header.Redacted {
			row["_clambhook_redacted"] = true
		}
		if header.Truncated {
			row["_clambhook_truncated"] = true
		}
		out = append(out, row)
	}
	return out
}

func harQueryString(rawURL string) []map[string]any {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return []map[string]any{}
	}
	values := parsed.Query()
	out := make([]map[string]any, 0, len(values))
	for name, vals := range values {
		for _, value := range vals {
			out = append(out, map[string]any{
				"name":  name,
				"value": value,
			})
		}
	}
	return out
}

func harCookies(cookies []Cookie) []map[string]any {
	out := make([]map[string]any, 0, len(cookies))
	for _, cookie := range cookies {
		row := map[string]any{
			"name":  cookie.Name,
			"value": cookie.Value,
		}
		if cookie.Redacted {
			row["_clambhook_redacted"] = true
		}
		if cookie.Domain != "" {
			row["domain"] = cookie.Domain
		}
		if cookie.Path != "" {
			row["path"] = cookie.Path
		}
		if cookie.Expires != "" {
			row["expires"] = cookie.Expires
		}
		if cookie.HTTPOnly {
			row["httpOnly"] = true
		}
		if cookie.Secure {
			row["secure"] = true
		}
		if cookie.SameSite != "" {
			row["sameSite"] = cookie.SameSite
		}
		out = append(out, row)
	}
	return out
}

func harPostData(body Body) map[string]any {
	row := map[string]any{
		"mimeType": body.MimeType,
		"text":     harBodyText(body),
		"_clambhook": map[string]any{
			"size":            body.Size,
			"preview_bytes":   body.PreviewBytes,
			"truncated":       body.Truncated,
			"truncated_after": body.TruncatedAfter,
			"encoding":        body.Encoding,
		},
	}
	if body.Encoding == "base64" {
		row["encoding"] = "base64"
	}
	return row
}

func harContent(body Body) map[string]any {
	row := map[string]any{
		"size":     body.Size,
		"mimeType": body.MimeType,
		"text":     harBodyText(body),
		"_clambhook": map[string]any{
			"preview_bytes":   body.PreviewBytes,
			"truncated":       body.Truncated,
			"truncated_after": body.TruncatedAfter,
			"encoding":        body.Encoding,
		},
	}
	if body.Encoding == "base64" {
		row["encoding"] = "base64"
	}
	return row
}

func harBodyText(body Body) string {
	if body.Encoding == "base64" {
		return body.PreviewBase64
	}
	return body.Preview
}
