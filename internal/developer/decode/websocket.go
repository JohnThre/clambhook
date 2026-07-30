package decode

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// wsOpcodeName maps a WebSocket opcode to a readable label.
func wsOpcodeName(op byte) string {
	switch op {
	case 0x0:
		return "continuation"
	case 0x1:
		return "text"
	case 0x2:
		return "binary"
	case 0x8:
		return "close"
	case 0x9:
		return "ping"
	case 0xA:
		return "pong"
	default:
		return "opcode-0x" + strconv.FormatInt(int64(op), 16)
	}
}

// WebSocket decodes a captured WebSocket byte stream (RFC 6455) into frames.
// direction labels which side produced the bytes. It never panics: any
// malformed or partial trailing frame stops decoding and returns whatever
// frames were parsed so far. A nil result means nothing decodable was found.
func WebSocket(data []byte, direction string) []Frame {
	if len(data) < 2 {
		return nil
	}
	var frames []Frame
	i := 0
	for i+2 <= len(data) && len(frames) < maxFrames {
		b0 := data[i]
		b1 := data[i+1]
		opcode := b0 & 0x0F
		masked := b1&0x80 != 0
		length := uint64(b1 & 0x7F)
		off := i + 2

		switch length {
		case 126:
			if off+2 > len(data) {
				return frames
			}
			length = uint64(binary.BigEndian.Uint16(data[off : off+2]))
			off += 2
		case 127:
			if off+8 > len(data) {
				return frames
			}
			length = binary.BigEndian.Uint64(data[off : off+8])
			off += 8
		}

		var maskKey []byte
		if masked {
			if off+4 > len(data) {
				return frames
			}
			maskKey = data[off : off+4]
			off += 4
		}

		// Guard against an absurd advertised length on a truncated capture:
		// only consume what is actually present.
		avail := len(data) - off
		truncated := false
		take := int(length)
		if length > uint64(avail) {
			take = avail
			truncated = true
		}
		if take < 0 {
			return frames
		}
		payload := make([]byte, take)
		copy(payload, data[off:off+take])
		if masked && len(maskKey) == 4 {
			for j := range payload {
				payload[j] ^= maskKey[j%4]
			}
		}

		frames = append(frames, wsFrame(opcode, payload, direction, truncated))
		if truncated {
			return frames
		}
		i = off + take
	}
	return frames
}

// wsFrame builds a Frame from a decoded WebSocket payload, rendering text
// payloads directly and binary payloads as a size summary.
func wsFrame(opcode byte, payload []byte, direction string, truncated bool) Frame {
	f := Frame{Direction: direction, Opcode: wsOpcodeName(opcode), Truncated: truncated}
	if opcode == 0x1 && utf8.Valid(payload) {
		preview, cut := clampPreview(string(payload))
		f.Preview = preview
		f.Truncated = f.Truncated || cut
		return f
	}
	if len(payload) == 0 {
		f.Preview = "(empty)"
		return f
	}
	if utf8.Valid(payload) {
		preview, cut := clampPreview(string(payload))
		f.Preview = preview
		f.Truncated = f.Truncated || cut
		return f
	}
	f.Preview = fmt.Sprintf("(%d bytes binary)", len(payload))
	return f
}
