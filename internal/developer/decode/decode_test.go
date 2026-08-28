// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package decode

import (
	"encoding/binary"
	"strings"
	"testing"
)

// buildWSFrame builds a single (unmasked) WebSocket frame for testing.
func buildWSFrame(opcode byte, payload []byte) []byte {
	frame := []byte{0x80 | opcode}
	switch {
	case len(payload) < 126:
		frame = append(frame, byte(len(payload)))
	case len(payload) <= 0xFFFF:
		frame = append(frame, 126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		frame = append(frame, ext[:]...)
	default:
		frame = append(frame, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		frame = append(frame, ext[:]...)
	}
	return append(frame, payload...)
}

func TestWebSocketDecodesTextFrames(t *testing.T) {
	data := append(buildWSFrame(0x1, []byte("hello")), buildWSFrame(0x1, []byte("world"))...)
	frames := WebSocket(data, DirClient)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	if frames[0].Opcode != "text" || frames[0].Preview != "hello" {
		t.Fatalf("frame0 = %+v", frames[0])
	}
	if frames[1].Preview != "world" || frames[1].Direction != DirClient {
		t.Fatalf("frame1 = %+v", frames[1])
	}
}

func TestWebSocketMaskedFrame(t *testing.T) {
	payload := []byte("ping!")
	mask := []byte{0x01, 0x02, 0x03, 0x04}
	frame := []byte{0x81, 0x80 | byte(len(payload))}
	frame = append(frame, mask...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	frame = append(frame, masked...)

	frames := WebSocket(frame, DirClient)
	if len(frames) != 1 || frames[0].Preview != "ping!" {
		t.Fatalf("masked decode = %+v", frames)
	}
}

func TestWebSocketBinaryFrame(t *testing.T) {
	frames := WebSocket(buildWSFrame(0x2, []byte{0x00, 0x01, 0xFF}), DirServer)
	if len(frames) != 1 || frames[0].Opcode != "binary" {
		t.Fatalf("binary decode = %+v", frames)
	}
	if !strings.Contains(frames[0].Preview, "bytes binary") {
		t.Fatalf("binary preview = %q", frames[0].Preview)
	}
}

func TestWebSocketTruncatedDoesNotPanic(t *testing.T) {
	// Frame header advertises 100 bytes but only 3 are present.
	data := []byte{0x81, 100, 'a', 'b', 'c'}
	frames := WebSocket(data, DirServer)
	if len(frames) != 1 || !frames[0].Truncated {
		t.Fatalf("truncated frame = %+v", frames)
	}
}

func TestWebSocketGarbageReturnsBounded(t *testing.T) {
	// Random bytes must never panic; result is best-effort.
	_ = WebSocket([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, DirClient)
}

// buildGRPCMessage frames a payload as a gRPC length-prefixed message.
func buildGRPCMessage(compressed byte, payload []byte) []byte {
	out := []byte{compressed}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	out = append(out, lenBuf[:]...)
	return append(out, payload...)
}

// buildProtobufString encodes a single length-delimited string field.
func buildProtobufString(fieldNum int, value string) []byte {
	tag := uint64(fieldNum)<<3 | 2
	var out []byte
	var tagBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tagBuf[:], tag)
	out = append(out, tagBuf[:n]...)
	var lenBuf [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(lenBuf[:], uint64(len(value)))
	out = append(out, lenBuf[:m]...)
	return append(out, []byte(value)...)
}

func TestGRPCDecodesMessage(t *testing.T) {
	msg := buildProtobufString(1, "clambhook")
	data := buildGRPCMessage(0, msg)
	frames := GRPC(data, DirClient)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if !strings.Contains(frames[0].Preview, "field 1") || !strings.Contains(frames[0].Preview, "clambhook") {
		t.Fatalf("grpc preview = %q", frames[0].Preview)
	}
}

func TestGRPCCompressedMessage(t *testing.T) {
	data := buildGRPCMessage(1, []byte{0x01, 0x02, 0x03})
	frames := GRPC(data, DirServer)
	if len(frames) != 1 || !strings.Contains(frames[0].Opcode, "compressed") {
		t.Fatalf("compressed grpc = %+v", frames)
	}
}

func TestGRPCTruncatedDoesNotPanic(t *testing.T) {
	// Advertises 50 bytes but only provides 2.
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x32, 'x', 'y'}
	frames := GRPC(data, DirServer)
	if len(frames) != 1 || !frames[0].Truncated {
		t.Fatalf("truncated grpc = %+v", frames)
	}
}

func TestGRPCGarbageIsSafe(t *testing.T) {
	_ = GRPC([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, DirClient)
}

func TestGraphQLRequest(t *testing.T) {
	body := []byte(`{"operationName":"GetUser","query":"query GetUser { user { id name } }","variables":{"id":"7"}}`)
	frames := GraphQL(body, DirClient)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	p := frames[0].Preview
	if !strings.Contains(p, "operation: GetUser") || !strings.Contains(p, "query:") || !strings.Contains(p, "variables:") {
		t.Fatalf("graphql request preview = %q", p)
	}
}

func TestGraphQLResponse(t *testing.T) {
	body := []byte(`{"data":{"user":{"id":"7","name":"Ada"}}}`)
	frames := GraphQL(body, DirServer)
	if len(frames) != 1 || frames[0].Opcode != "response" {
		t.Fatalf("graphql response = %+v", frames)
	}
	if !strings.Contains(frames[0].Preview, "data:") || !strings.Contains(frames[0].Preview, "Ada") {
		t.Fatalf("graphql response preview = %q", frames[0].Preview)
	}
}

func TestGraphQLErrorsResponse(t *testing.T) {
	body := []byte(`{"errors":[{"message":"boom"}],"data":null}`)
	frames := GraphQL(body, DirServer)
	if len(frames) != 1 || !strings.Contains(frames[0].Preview, "errors:") {
		t.Fatalf("graphql errors = %+v", frames)
	}
}

func TestGraphQLNonMatchingReturnsNil(t *testing.T) {
	if frames := GraphQL([]byte(`{"hello":"world"}`), DirClient); frames != nil {
		t.Fatalf("expected nil for non-graphql request, got %+v", frames)
	}
	if frames := GraphQL([]byte("not json"), DirServer); frames != nil {
		t.Fatalf("expected nil for non-json, got %+v", frames)
	}
}
