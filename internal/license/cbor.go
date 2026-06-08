package license

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type cborValue any

type cborReader struct {
	data []byte
	pos  int
}

func decodeCBOR(data []byte) (cborValue, error) {
	r := &cborReader{data: data}
	v, err := r.read()
	if err != nil {
		return nil, err
	}
	if r.pos != len(data) {
		return nil, errors.New("trailing CBOR data")
	}
	return v, nil
}

func (r *cborReader) read() (cborValue, error) {
	if r.pos >= len(r.data) {
		return nil, errors.New("unexpected end of CBOR data")
	}
	b := r.data[r.pos]
	r.pos++
	major := b >> 5
	add := b & 0x1f
	n, err := r.readLen(add)
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		return n, nil
	case 1:
		return -1 - int64(n), nil
	case 2:
		return r.readBytes(n)
	case 3:
		bytes, err := r.readBytes(n)
		if err != nil {
			return nil, err
		}
		return string(bytes), nil
	case 4:
		items := make([]cborValue, 0, n)
		for i := uint64(0); i < n; i++ {
			item, err := r.read()
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	case 5:
		m := make(map[string]cborValue, n)
		for i := uint64(0); i < n; i++ {
			key, err := r.read()
			if err != nil {
				return nil, err
			}
			keyString, ok := key.(string)
			if !ok {
				return nil, errors.New("CBOR map key must be string")
			}
			value, err := r.read()
			if err != nil {
				return nil, err
			}
			m[keyString] = value
		}
		return m, nil
	case 7:
		switch add {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22:
			return nil, nil
		default:
			return nil, fmt.Errorf("unsupported CBOR simple value %d", add)
		}
	default:
		return nil, fmt.Errorf("unsupported CBOR major type %d", major)
	}
}

func (r *cborReader) readLen(add byte) (uint64, error) {
	switch {
	case add < 24:
		return uint64(add), nil
	case add == 24:
		if r.pos+1 > len(r.data) {
			return 0, errors.New("short CBOR uint8")
		}
		n := r.data[r.pos]
		r.pos++
		return uint64(n), nil
	case add == 25:
		if r.pos+2 > len(r.data) {
			return 0, errors.New("short CBOR uint16")
		}
		n := binary.BigEndian.Uint16(r.data[r.pos:])
		r.pos += 2
		return uint64(n), nil
	case add == 26:
		if r.pos+4 > len(r.data) {
			return 0, errors.New("short CBOR uint32")
		}
		n := binary.BigEndian.Uint32(r.data[r.pos:])
		r.pos += 4
		return uint64(n), nil
	case add == 27:
		if r.pos+8 > len(r.data) {
			return 0, errors.New("short CBOR uint64")
		}
		n := binary.BigEndian.Uint64(r.data[r.pos:])
		r.pos += 8
		return n, nil
	default:
		return 0, fmt.Errorf("unsupported CBOR length additional info %d", add)
	}
}

func (r *cborReader) readBytes(n uint64) ([]byte, error) {
	if n > uint64(len(r.data)-r.pos) {
		return nil, errors.New("short CBOR bytes")
	}
	out := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func cborMap(v cborValue) (map[string]cborValue, bool) {
	m, ok := v.(map[string]cborValue)
	return m, ok
}

func cborBytes(v cborValue) ([]byte, bool) {
	b, ok := v.([]byte)
	return b, ok
}

func cborString(v cborValue) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func cborArray(v cborValue) ([]cborValue, bool) {
	a, ok := v.([]cborValue)
	return a, ok
}
