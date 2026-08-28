package developer

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

// Body viewer caps. The pretty-printer never feeds more than viewerPrettyLimit
// bytes to a formatter (the captured preview is already bounded by
// BodyLimitBytes, so this is only a safety net for raised limits). The hex
// dump is capped at viewerHexBytes so a single entry cannot balloon the JSON
// response; longer bodies show a "... (N more bytes)" trailer.
const (
	viewerPrettyLimit = 256 << 10
	viewerHexBytes    = 8 << 10
)

// computeViewer derives the shared pretty/hex viewer for a captured body from
// its preview bytes and MIME type. It is best-effort and panic-safe.
//
// Hex is always populated for non-empty bodies (even binary ones). Pretty is
// only produced for UTF-8 bodies that parse as JSON/XML/form/HTML; binary or
// unrecognized bodies get Kind "text" and no Pretty, so clients fall back to
// the raw Preview. An empty body yields a nil Viewer (no viewer applies).
func computeViewer(mimeType string, preview []byte, utf8Body, truncated bool) *BodyViewer {
	if len(preview) == 0 {
		return nil
	}
	v := &BodyViewer{Hex: hexDump(preview, viewerHexBytes)}
	if !utf8Body {
		v.Kind = "text"
		return v
	}
	prettyInput := preview
	prettyTrunc := truncated
	if len(prettyInput) > viewerPrettyLimit {
		prettyInput = prettyInput[:viewerPrettyLimit]
		prettyTrunc = true
	}
	if kind, pretty, ok := prettyBody(mimeType, prettyInput); ok {
		v.Kind = kind
		v.Pretty = pretty
		v.PrettyTruncated = prettyTrunc
		return v
	}
	v.Kind = "text"
	return v
}

// prettyBody attempts to pretty-print data according to its MIME type (or, for
// generic/missing content types, a leading-byte sniff). It returns the kind,
// the re-indented text, and whether pretty-printing succeeded. Any failure
// yields ok=false so the caller falls back to the raw preview.
func prettyBody(mimeType string, data []byte) (kind, pretty string, ok bool) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "", "", false
	}
	for _, c := range bodyCandidates(contentTypeBase(mimeType), trimmed) {
		switch c {
		case "json":
			if p, err := prettyJSONBytes(trimmed); err == nil {
				return "json", p, true
			}
		case "xml":
			if p, err := prettyXML(trimmed); err == nil {
				return "xml", p, true
			}
		case "form":
			if p, okForm := prettyForm(trimmed); okForm {
				return "form", p, true
			}
		case "html":
			if p, okHTML := prettyHTML(trimmed); okHTML {
				return "html", p, true
			}
		}
	}
	return "text", "", false
}

// bodyCandidates returns the pretty-print kinds to try, in priority order,
// based on the content type and a leading-byte sniff when the content type is
// generic or missing. Returning nil means no structured kind applies (text).
func bodyCandidates(mimeType string, data []byte) []string {
	switch mimeType {
	case "application/json", "text/json", "application/json-patch+json", "application/vnd.api+json":
		return []string{"json"}
	case "application/xml", "text/xml", "application/atom+xml", "application/rss+xml", "application/soap+xml", "application/xml-dtd":
		return []string{"xml"}
	case "application/x-www-form-urlencoded":
		return []string{"form"}
	case "text/html", "application/xhtml+xml":
		return []string{"html"}
	}
	first := firstNonSpaceByte(data)
	switch first {
	case '{', '[':
		return []string{"json"}
	case '<':
		lower := bytes.ToLower(bytes.TrimSpace(data))
		switch {
		case bytes.HasPrefix(lower, []byte("<!doctype html")), bytes.HasPrefix(lower, []byte("<html")):
			return []string{"html", "xml"}
		case bytes.HasPrefix(lower, []byte("<?xml")):
			return []string{"xml"}
		default:
			return []string{"xml", "html"}
		}
	}
	// form sniff: key=value pairs, single line, URL-decodable, no markup.
	if bytes.ContainsRune(data, '=') && !bytes.ContainsRune(data, '\n') && !bytes.ContainsRune(data, '<') {
		return []string{"form"}
	}
	return nil
}

func prettyJSONBytes(data []byte) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func prettyXML(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		// Drop existing whitespace-only character data; the encoder re-indents.
		if cd, ok := tok.(xml.CharData); ok && len(bytes.TrimSpace(cd)) == 0 {
			continue
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", err
		}
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func prettyForm(data []byte) (string, bool) {
	values, err := url.ParseQuery(string(data))
	if err != nil || len(values) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range values[k] {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}
	return strings.TrimRight(b.String(), "\n"), true
}

// prettyHTML is a small, conservative stdlib-only HTML indenter. It walks the
// byte stream, splits on '<'...'>', and indents start/end tags. It does not
// restructure attributes and intentionally declines raw-text / RCDATA elements
// (script, style, textarea) where '<' does not start a tag, returning false so
// the caller falls back to the raw preview rather than risk mangling.
func prettyHTML(data []byte) (string, bool) {
	text := strings.TrimSpace(string(data))
	if text == "" || !strings.Contains(text, "<") {
		return "", false
	}
	lower := strings.ToLower(text)
	for _, raw := range []string{"<script", "<style", "<textarea"} {
		if strings.Contains(lower, raw) {
			return "", false
		}
	}
	var b strings.Builder
	depth := 0
	i := 0
	for i < len(text) {
		if text[i] == '<' {
			// Skip comments entirely.
			if strings.HasPrefix(text[i:], "<!--") {
				end := strings.Index(text[i:], "-->")
				if end < 0 {
					return "", false
				}
				writeIndentedLine(&b, depth, text[i:i+end+3])
				i += end + 3
				continue
			}
			end := strings.IndexByte(text[i:], '>')
			if end < 0 {
				return "", false
			}
			tag := text[i : i+end+1]
			inner := strings.TrimSpace(tag[1 : len(tag)-1])
			name := htmlTagName(inner)
			switch {
			case strings.HasPrefix(inner, "!"), strings.HasPrefix(inner, "?"):
				writeIndentedLine(&b, depth, tag)
			case strings.HasPrefix(inner, "/"):
				if depth > 0 {
					depth--
				}
				writeIndentedLine(&b, depth, tag)
			case strings.HasSuffix(inner, "/") || isVoidHTMLTag(name):
				writeIndentedLine(&b, depth, tag)
			default:
				writeIndentedLine(&b, depth, tag)
				depth++
			}
			i += end + 1
		} else {
			j := strings.IndexByte(text[i:], '<')
			if j < 0 {
				j = len(text) - i
			}
			if chunk := strings.TrimSpace(text[i : i+j]); chunk != "" {
				writeIndentedLine(&b, depth, chunk)
			}
			i += j
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return "", false
	}
	return out, true
}

func writeIndentedLine(b *strings.Builder, depth int, s string) {
	for d := 0; d < depth; d++ {
		b.WriteString("  ")
	}
	b.WriteString(s)
	b.WriteByte('\n')
}

func htmlTagName(inner string) string {
	name := inner
	if strings.HasPrefix(name, "/") {
		name = name[1:]
	}
	if sp := strings.IndexByte(name, ' '); sp >= 0 {
		name = name[:sp]
	}
	return strings.ToLower(name)
}

func isVoidHTMLTag(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// hexDump renders a classic offset | hex | ascii dump, capped at limit bytes
// with a trailing "... (N more bytes)" line when truncated.
func hexDump(data []byte, limit int) string {
	if len(data) == 0 {
		return ""
	}
	dump := data
	trunc := false
	if len(dump) > limit {
		dump = dump[:limit]
		trunc = true
	}
	var b strings.Builder
	for offset := 0; offset < len(dump); offset += 16 {
		end := offset + 16
		if end > len(dump) {
			end = len(dump)
		}
		row := dump[offset:end]
		fmt.Fprintf(&b, "%08x  ", offset)
		for i := 0; i < 16; i++ {
			if i < len(row) {
				fmt.Fprintf(&b, "%02x ", row[i])
			} else {
				b.WriteString("   ")
			}
			if i == 7 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(" |")
		for _, c := range row {
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	if trunc {
		fmt.Fprintf(&b, "... (%d more bytes)\n", len(data)-limit)
	}
	return b.String()
}

func contentTypeBase(mimeType string) string {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}

func firstNonSpaceByte(data []byte) byte {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b
		}
	}
	return 0
}
