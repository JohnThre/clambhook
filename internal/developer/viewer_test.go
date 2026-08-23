package developer

import (
	"strings"
	"testing"
)

func TestComputeViewerEmptyReturnsNil(t *testing.T) {
	if v := computeViewer("", nil, true, false); v != nil {
		t.Fatalf("empty body viewer = %+v, want nil", v)
	}
}

func TestComputeViewerJSON(t *testing.T) {
	v := computeViewer("application/json", []byte(`{"a":1,"b":[2,3]}`), true, false)
	if v == nil {
		t.Fatal("nil viewer")
	}
	if v.Kind != "json" {
		t.Fatalf("kind = %q, want json", v.Kind)
	}
	if !strings.Contains(v.Pretty, `"a": 1`) || !strings.Contains(v.Pretty, `"b": [`) {
		t.Fatalf("pretty not indented: %q", v.Pretty)
	}
	if v.PrettyTruncated {
		t.Fatalf("unexpected pretty truncated")
	}
	if v.Hex == "" {
		t.Fatal("hex missing")
	}
}

func TestComputeViewerJSONSniffedFromGenericContentType(t *testing.T) {
	// text/plain content type but JSON body should sniff to json.
	v := computeViewer("text/plain", []byte(`[1,2,3]`), true, false)
	if v.Kind != "json" {
		t.Fatalf("kind = %q, want json via sniff", v.Kind)
	}
}

func TestComputeViewerJSONInvalidFallsBackToText(t *testing.T) {
	v := computeViewer("application/json", []byte(`{not json`), true, false)
	if v.Kind != "text" {
		t.Fatalf("kind = %q, want text fallback", v.Kind)
	}
	if v.Pretty != "" {
		t.Fatalf("invalid json should not produce pretty: %q", v.Pretty)
	}
	if v.Hex == "" {
		t.Fatal("hex still expected for invalid-but-utf8 body")
	}
}

func TestComputeViewerXML(t *testing.T) {
	v := computeViewer("application/xml", []byte(`<root><a>1</a><b>2</b></root>`), true, false)
	if v.Kind != "xml" {
		t.Fatalf("kind = %q, want xml", v.Kind)
	}
	// The encoder re-indents; expect a newline between elements.
	if !strings.Contains(v.Pretty, "<root>\n") {
		t.Fatalf("xml not indented: %q", v.Pretty)
	}
}

func TestComputeViewerXMLDeclarationPreserved(t *testing.T) {
	v := computeViewer("application/xml", []byte(`<?xml version="1.0"?><root><a/></root>`), true, false)
	if v.Kind != "xml" {
		t.Fatalf("kind = %q", v.Kind)
	}
	if !strings.Contains(v.Pretty, "<?xml") {
		t.Fatalf("declaration dropped: %q", v.Pretty)
	}
}

func TestComputeViewerForm(t *testing.T) {
	v := computeViewer("application/x-www-form-urlencoded", []byte(`a=1&b=two+words&c=3`), true, false)
	if v.Kind != "form" {
		t.Fatalf("kind = %q, want form", v.Kind)
	}
	if !strings.Contains(v.Pretty, "a = 1") || !strings.Contains(v.Pretty, "b = two words") {
		t.Fatalf("form not parsed: %q", v.Pretty)
	}
}

func TestComputeViewerHTMLSkipsRawTextElements(t *testing.T) {
	// Bodies containing script/style must not be mangled; fall back to text.
	v := computeViewer("text/html", []byte(`<html><body><script>if (a < b) {}</script></body></html>`), true, false)
	if v.Kind != "text" {
		t.Fatalf("kind = %q, want text fallback for script body", v.Kind)
	}
}

func TestComputeViewerHTMLIndents(t *testing.T) {
	v := computeViewer("text/html", []byte(`<html><body><p>hi</p></body></html>`), true, false)
	if v.Kind != "html" {
		t.Fatalf("kind = %q, want html", v.Kind)
	}
	// Indentation should nest <p> deeper than <body>.
	if !strings.Contains(v.Pretty, "    <p>") {
		t.Fatalf("html not indented: %q", v.Pretty)
	}
}

func TestComputeViewerBinaryGetsHexOnly(t *testing.T) {
	v := computeViewer("application/octet-stream", []byte{0x00, 0x01, 0x02, 'a', 0xff}, false, false)
	if v == nil {
		t.Fatal("nil viewer")
	}
	if v.Kind != "text" {
		t.Fatalf("kind = %q, want text for binary", v.Kind)
	}
	if v.Pretty != "" {
		t.Fatalf("binary should not get pretty: %q", v.Pretty)
	}
	if !strings.Contains(v.Hex, "00 01 02") || !strings.Contains(v.Hex, "|...a.|") {
		t.Fatalf("hex dump wrong: %q", v.Hex)
	}
}

func TestComputeViewerHexTruncation(t *testing.T) {
	// Body larger than the hex cap gets a "... more bytes" trailer.
	big := make([]byte, viewerHexBytes+32)
	for i := range big {
		big[i] = 'A'
	}
	v := computeViewer("text/plain", big, true, false)
	if !strings.Contains(v.Hex, "more bytes)") {
		t.Fatalf("hex missing truncation trailer: %q", v.Hex)
	}
}

func TestComputeViewerPrettyTruncatedFlag(t *testing.T) {
	// A truncated body (total > preview) marks the pretty preview truncated.
	v := computeViewer("application/json", []byte(`{"a":1}`), true, true)
	if !v.PrettyTruncated {
		t.Fatalf("expected pretty truncated flag, got %+v", v)
	}
}

func TestHexDumpFormat(t *testing.T) {
	out := hexDump([]byte("AB"), 64)
	if !strings.Contains(out, "00000000") {
		t.Fatalf("missing offset: %q", out)
	}
	if !strings.Contains(out, "41 42") {
		t.Fatalf("missing hex bytes: %q", out)
	}
	if !strings.Contains(out, "|AB|") {
		t.Fatalf("missing ascii: %q", out)
	}
}

func TestPrettyJSONBytesRejectsInvalid(t *testing.T) {
	if _, err := prettyJSONBytes([]byte(`{not`)); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestBodyCandidatesContentTypePriority(t *testing.T) {
	// application/json content type yields only the json candidate.
	if got := bodyCandidates("application/json", []byte(`{}`)); len(got) != 1 || got[0] != "json" {
		t.Fatalf("json candidates = %+v", got)
	}
	// generic sniff by leading byte.
	if got := bodyCandidates("", []byte(`{`)); len(got) != 1 || got[0] != "json" {
		t.Fatalf("sniff json = %+v", got)
	}
	if got := bodyCandidates("", []byte(`<html>`)); len(got) < 1 || got[0] != "html" {
		t.Fatalf("sniff html = %+v", got)
	}
	if got := bodyCandidates("", []byte(`a=1&b=2`)); len(got) != 1 || got[0] != "form" {
		t.Fatalf("sniff form = %+v", got)
	}
	if got := bodyCandidates("", []byte(`plain text`)); got != nil {
		t.Fatalf("plain text should yield nil candidates, got %+v", got)
	}
}
