// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package decode turns captured application-protocol byte streams into
// structured, human-readable frames for the capture detail viewers. It decodes
// WebSocket frames, gRPC length-prefixed messages, and GraphQL request/response
// bodies daemon-side so all four clients render a single shared shape.
//
// Every decoder is bounded and panic-safe: it caps the number and size of
// emitted frames and, on any malformed input, degrades gracefully rather than
// returning an error the caller must special-case. Callers fall back to the
// existing raw body preview when a decoder yields nothing.
package decode

const (
	// KindWebSocket, KindGRPC, and KindGraphQL are the decoded stream kinds.
	KindWebSocket = "websocket"
	KindGRPC      = "grpc"
	KindGraphQL   = "graphql"

	// DirClient and DirServer label a frame's origin.
	DirClient = "client"
	DirServer = "server"

	// maxFrames bounds how many frames a single decode emits so a chatty
	// stream cannot balloon a capture entry.
	maxFrames = 200
	// maxPreview bounds an individual frame preview in bytes.
	maxPreview = 4096
)

// Frame is one decoded protocol message.
type Frame struct {
	Direction string `json:"direction"`
	Opcode    string `json:"opcode,omitempty"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Result is a decoded protocol stream.
type Result struct {
	Kind   string  `json:"kind"`
	Frames []Frame `json:"frames,omitempty"`
}

// clampPreview trims s to maxPreview bytes, reporting whether it was cut.
func clampPreview(s string) (string, bool) {
	if len(s) <= maxPreview {
		return s, false
	}
	return s[:maxPreview], true
}
