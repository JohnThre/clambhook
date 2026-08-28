// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package developer

import (
	"errors"
	"fmt"
	"strings"
)

// ParsedCurl is a cURL command parsed into the request fields a compose window
// consumes. Headers reuse the capture Header type; redacted/truncated are
// always false here (a parsed cURL has no redaction state).
type ParsedCurl struct {
	Method  string   `json:"method"`
	URL     string   `json:"url"`
	Headers []Header `json:"headers"`
	Body    string   `json:"body"`
}

// entryToCurl serializes a captured transaction back to a runnable cURL
// command. Redacted headers are omitted (with a comment naming them); Content-
// Length and Host are dropped because curl recomputes them. When the captured
// request body was truncated, a trailing comment warns the operator to supply
// the full body. All values are single-quoted with '\” escaping.
func entryToCurl(e Entry) string {
	method := strings.TrimSpace(e.Method)
	if method == "" {
		method = "GET"
	}
	var b strings.Builder
	b.WriteString("curl")
	if method != "GET" {
		b.WriteString(" -X ")
		b.WriteString(shellSingleQuote(method))
	}
	skip := map[string]struct{}{
		"content-length": {},
		"host":           {},
	}
	var redacted []string
	for _, h := range e.Request.Headers {
		if h.Redacted {
			redacted = append(redacted, h.Name)
			continue
		}
		if _, ok := skip[strings.ToLower(strings.TrimSpace(h.Name))]; ok {
			continue
		}
		b.WriteString(" -H ")
		b.WriteString(shellSingleQuote(h.Name + ": " + h.Value))
	}
	if body := e.Request.Body.Preview; body != "" {
		b.WriteString(" --data-raw ")
		b.WriteString(shellSingleQuote(body))
	}
	b.WriteString(" ")
	b.WriteString(shellSingleQuote(e.URL))
	if e.Request.Body.Truncated {
		b.WriteString("\n# warning: captured request body was truncated; supply the full body before sending")
	}
	if len(redacted) > 0 {
		b.WriteString("\n# redacted headers omitted: ")
		b.WriteString(strings.Join(redacted, ", "))
	}
	return b.String()
}

// curlToRepeat parses a cURL command into the request fields a compose window
// consumes. It is a hand-rolled, bounded, stdlib-only shell tokenizer (single
// and double quotes plus backslash escapes) followed by a flag walker. It
// supports the common subset (-X/--request, repeatable -H/--header, the -d/
// --data/--data-raw/--data-binary/--data-ascii body flags, -A/--user-agent,
// -e/--referer, --url, and benign flags like --compressed/-k/-L/-i/-s/-S/-v
// which are accepted and ignored). Unknown flags are ignored best-effort
// (Proxyman parity); only a value-taking flag missing its value, an @file
// body, an unterminated quote, or a missing URL error out. No new dependency.
func curlToRepeat(text string) (ParsedCurl, error) {
	toks, err := tokenizeShell(text)
	if err != nil {
		return ParsedCurl{}, err
	}
	if len(toks) > 0 {
		first := strings.ToLower(toks[0])
		if first == "curl" || strings.HasSuffix(first, "/curl") {
			toks = toks[1:]
		}
	}
	out := ParsedCurl{Method: "GET"}
	var data []string
	methodSet := false
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t == "-X" || t == "--request":
			if i+1 >= len(toks) {
				return ParsedCurl{}, errors.New("-X/--request requires a value")
			}
			out.Method = strings.ToUpper(strings.TrimSpace(toks[i+1]))
			methodSet = true
			i++
		case t == "-H" || t == "--header":
			if i+1 >= len(toks) {
				return ParsedCurl{}, errors.New("-H/--header requires a value")
			}
			out.Headers = append(out.Headers, parseCurlHeader(toks[i+1]))
			i++
		case t == "-d" || t == "--data" || t == "--data-raw" || t == "--data-binary" || t == "--data-ascii":
			if i+1 >= len(toks) {
				return ParsedCurl{}, fmt.Errorf("%s requires a value", t)
			}
			val := toks[i+1]
			if strings.HasPrefix(val, "@") {
				return ParsedCurl{}, fmt.Errorf("%s @file is not supported; paste the body inline", t)
			}
			data = append(data, val)
			i++
		case t == "-A" || t == "--user-agent":
			if i+1 >= len(toks) {
				return ParsedCurl{}, fmt.Errorf("%s requires a value", t)
			}
			out.Headers = append(out.Headers, Header{Name: "User-Agent", Value: toks[i+1]})
			i++
		case t == "-e" || t == "--referer":
			if i+1 >= len(toks) {
				return ParsedCurl{}, fmt.Errorf("%s requires a value", t)
			}
			out.Headers = append(out.Headers, Header{Name: "Referer", Value: toks[i+1]})
			i++
		case t == "--url":
			if i+1 >= len(toks) {
				return ParsedCurl{}, errors.New("--url requires a value")
			}
			out.URL = toks[i+1]
			i++
		case t == "-o" || t == "--output" || t == "--connect-timeout" || t == "-m" || t == "--max-time" ||
			t == "--retry" || t == "-u" || t == "--user" || t == "-b" || t == "--cookie" ||
			t == "-x" || t == "--proxy" || t == "-U" || t == "--proxy-user" || t == "--resolve" || t == "--dns-servers":
			// Known value-taking flags we otherwise ignore: consume the value
			// so it is not mistaken for the URL.
			if i+1 >= len(toks) {
				return ParsedCurl{}, fmt.Errorf("%s requires a value", t)
			}
			i++
		case t == "--compressed" || t == "-k" || t == "--insecure" || t == "-L" || t == "--location" ||
			t == "-i" || t == "--include" || t == "-s" || t == "--silent" || t == "-S" || t == "--show-error" ||
			t == "-v" || t == "--verbose" || t == "-G" || t == "--get" || t == "-I" || t == "--head":
			// benign boolean flags: accepted, ignored
		case strings.HasPrefix(t, "-"):
			// unknown flag: best-effort ignore (Proxyman parity). Assume it
			// takes no value; if it does, its value may be read as the URL,
			// which the last-URL-wins rule usually corrects.
		default:
			// a URL. curl uses the last URL; keep overwriting so the final one
			// wins, matching curl behavior.
			out.URL = t
		}
	}
	if len(data) > 0 {
		out.Body = strings.Join(data, "&")
		if !methodSet {
			out.Method = "POST"
		}
	}
	if out.URL == "" {
		return ParsedCurl{}, errors.New("cURL command has no URL")
	}
	return out, nil
}

func parseCurlHeader(s string) Header {
	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return Header{Name: strings.TrimSpace(s)}
	}
	return Header{Name: strings.TrimSpace(s[:idx]), Value: strings.TrimSpace(s[idx+1:])}
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes
// as the POSIX '\” sequence so the result is safe to paste into a shell.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// tokenizeShell splits a shell-like command line into tokens, honoring single
// quotes (literal), double quotes (backslash escapes $ ` " \ newline), and
// backslash escapes outside quotes. Whitespace separates tokens. An
// unterminated quote yields an error so callers can surface a 400 rather than
// silently truncating a request.
func tokenizeShell(s string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	singleQ, doubleQ := false, false
	hasToken := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case singleQ:
			if c == '\'' {
				singleQ = false
			} else {
				cur.WriteByte(c)
			}
		case doubleQ:
			switch {
			case c == '"':
				doubleQ = false
			case c == '\\' && i+1 < len(s):
				next := s[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' || next == '\n' {
					cur.WriteByte(next)
					i++
				} else {
					cur.WriteByte(c)
				}
			default:
				cur.WriteByte(c)
			}
		case c == '\'':
			singleQ = true
			hasToken = true
		case c == '"':
			doubleQ = true
			hasToken = true
		case c == '\\':
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
				hasToken = true
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if hasToken {
				toks = append(toks, cur.String())
				cur.Reset()
				hasToken = false
			}
		default:
			cur.WriteByte(c)
			hasToken = true
		}
	}
	if singleQ || doubleQ {
		return nil, errors.New("unterminated quote in cURL command")
	}
	if hasToken {
		toks = append(toks, cur.String())
	}
	return toks, nil
}
