// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package decode

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// GRPC decodes a gRPC length-prefixed message stream. Each message is framed as
// a 1-byte compression flag followed by a big-endian uint32 length and the
// protobuf payload. direction labels the producing side. Parsing is best-effort
// and panic-safe: a truncated trailing message is surfaced with Truncated set
// and decoding stops. A nil result means nothing decodable was found.
func GRPC(data []byte, direction string) []Frame {
	if len(data) < 5 {
		return nil
	}
	var frames []Frame
	i := 0
	for i+5 <= len(data) && len(frames) < maxFrames {
		compressed := data[i]
		length := binary.BigEndian.Uint32(data[i+1 : i+5])
		off := i + 5

		avail := len(data) - off
		truncated := false
		take := int(length)
		if uint64(length) > uint64(avail) {
			take = avail
			truncated = true
		}
		if take < 0 {
			return frames
		}
		payload := data[off : off+take]
		frames = append(frames, grpcFrame(compressed, payload, direction, truncated))
		if truncated {
			return frames
		}
		i = off + take
	}
	return frames
}

// grpcFrame renders a single gRPC message as a best-effort protobuf field dump.
func grpcFrame(compressed byte, payload []byte, direction string, truncated bool) Frame {
	f := Frame{Direction: direction, Opcode: "message", Truncated: truncated}
	if compressed != 0 {
		f.Opcode = "message (compressed)"
		f.Preview = fmt.Sprintf("(%d bytes compressed payload)", len(payload))
		return f
	}
	preview, cut := clampPreview(protobufDump(payload))
	f.Preview = preview
	f.Truncated = f.Truncated || cut
	return f
}

// protobufDump renders a protobuf wire-format message as a one-line-per-field
// summary. It is deliberately schema-less: without a .proto it can only report
// field numbers, wire types, and a value hint. On any malformed input it falls
// back to a raw byte summary rather than failing.
func protobufDump(data []byte) string {
	if len(data) == 0 {
		return "(empty message)"
	}
	var b strings.Builder
	i := 0
	fields := 0
	for i < len(data) && fields < 256 {
		tag, n := binary.Uvarint(data[i:])
		if n <= 0 {
			return protobufFallback(data)
		}
		i += n
		fieldNum := tag >> 3
		wireType := tag & 0x7
		if fieldNum == 0 {
			return protobufFallback(data)
		}
		if fields > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "field %d: ", fieldNum)
		switch wireType {
		case 0: // varint
			v, m := binary.Uvarint(data[i:])
			if m <= 0 {
				return protobufFallback(data)
			}
			i += m
			b.WriteString("varint " + strconv.FormatUint(v, 10))
		case 1: // 64-bit
			if i+8 > len(data) {
				return protobufFallback(data)
			}
			v := binary.LittleEndian.Uint64(data[i : i+8])
			i += 8
			b.WriteString("fixed64 " + strconv.FormatUint(v, 10))
		case 2: // length-delimited
			l, m := binary.Uvarint(data[i:])
			if m <= 0 {
				return protobufFallback(data)
			}
			i += m
			if uint64(i)+l > uint64(len(data)) {
				return protobufFallback(data)
			}
			seg := data[i : i+int(l)]
			i += int(l)
			if utf8.Valid(seg) && isPrintable(seg) {
				b.WriteString("string " + strconv.Quote(string(seg)))
			} else {
				fmt.Fprintf(&b, "bytes (%d)", len(seg))
			}
		case 5: // 32-bit
			if i+4 > len(data) {
				return protobufFallback(data)
			}
			v := binary.LittleEndian.Uint32(data[i : i+4])
			i += 4
			b.WriteString("fixed32 " + strconv.FormatUint(uint64(v), 10))
		default:
			return protobufFallback(data)
		}
		fields++
	}
	if b.Len() == 0 {
		return protobufFallback(data)
	}
	return b.String()
}

// protobufFallback summarizes a payload that could not be parsed as protobuf.
func protobufFallback(data []byte) string {
	if utf8.Valid(data) && isPrintable(data) {
		return string(data)
	}
	return fmt.Sprintf("(%d bytes protobuf)", len(data))
}

// isPrintable reports whether a byte slice is mostly printable text (allowing
// common whitespace), used to decide between string and byte rendering.
func isPrintable(data []byte) bool {
	for _, c := range data {
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if c < 0x20 && c != 0 {
			return false
		}
	}
	return true
}
