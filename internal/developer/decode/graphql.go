package decode

import (
	"bytes"
	"encoding/json"
	"strings"
)

// GraphQL decodes a GraphQL request or response body. A request body is a JSON
// object carrying a "query" (and optional "variables"/"operationName"); a
// response body is a JSON object carrying "data" and/or "errors". direction
// labels the producing side. It is panic-safe and returns nil when the body is
// not recognizable GraphQL JSON, so the caller falls back to a raw preview.
func GraphQL(data []byte, direction string) []Frame {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil
	}
	if direction == DirClient {
		return graphQLRequest(doc)
	}
	return graphQLResponse(doc)
}

// graphQLRequest renders the query, operation name, and variables of a request.
func graphQLRequest(doc map[string]json.RawMessage) []Frame {
	rawQuery, ok := doc["query"]
	if !ok {
		return nil
	}
	var query string
	if err := json.Unmarshal(rawQuery, &query); err != nil || strings.TrimSpace(query) == "" {
		return nil
	}
	var b strings.Builder
	if op, ok := doc["operationName"]; ok {
		var name string
		if json.Unmarshal(op, &name) == nil && name != "" {
			b.WriteString("operation: " + name + "\n\n")
		}
	}
	b.WriteString("query:\n")
	b.WriteString(strings.TrimRight(query, "\n"))
	if vars, ok := doc["variables"]; ok && len(bytes.TrimSpace(vars)) > 0 && !bytes.Equal(bytes.TrimSpace(vars), []byte("null")) {
		b.WriteString("\n\nvariables:\n")
		b.WriteString(prettyJSON(vars))
	}
	preview, cut := clampPreview(b.String())
	return []Frame{{Direction: DirClient, Opcode: "request", Preview: preview, Truncated: cut}}
}

// graphQLResponse renders the data and errors sections of a response.
func graphQLResponse(doc map[string]json.RawMessage) []Frame {
	dataRaw, hasData := doc["data"]
	errRaw, hasErr := doc["errors"]
	if !hasData && !hasErr {
		return nil
	}
	var b strings.Builder
	if hasErr && len(bytes.TrimSpace(errRaw)) > 0 && !bytes.Equal(bytes.TrimSpace(errRaw), []byte("null")) {
		b.WriteString("errors:\n")
		b.WriteString(prettyJSON(errRaw))
	}
	if hasData {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("data:\n")
		b.WriteString(prettyJSON(dataRaw))
	}
	if b.Len() == 0 {
		return nil
	}
	preview, cut := clampPreview(b.String())
	return []Frame{{Direction: DirServer, Opcode: "response", Preview: preview, Truncated: cut}}
}

// prettyJSON re-indents a JSON fragment, falling back to the raw text when it
// cannot be parsed.
func prettyJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
