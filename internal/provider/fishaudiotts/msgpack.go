package fishaudiotts

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	maxMsgpackUint8  = 1<<8 - 1
	maxMsgpackUint16 = 1<<16 - 1
	maxMsgpackUint32 = 1<<32 - 1
	minMsgpackInt8   = -1 << 7
	minMsgpackInt16  = -1 << 15
	minMsgpackInt32  = -1 << 31
)

func marshalMsgpack(value any) ([]byte, error) {
	var out []byte
	out, err := appendMsgpack(out, value)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func appendMsgpack(out []byte, value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return append(out, 0xc0), nil
	case bool:
		if v {
			return append(out, 0xc3), nil
		}
		return append(out, 0xc2), nil
	case string:
		return appendMsgpackString(out, v), nil
	case []byte:
		return appendMsgpackBinary(out, v), nil
	case int:
		return appendMsgpackInt(out, int64(v)), nil
	case int64:
		return appendMsgpackInt(out, v), nil
	case uint64:
		return appendMsgpackUint(out, v), nil
	case float64:
		out = append(out, 0xcb)
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(v))
		return append(out, buf[:]...), nil
	case map[string]any:
		return appendMsgpackMap(out, v)
	default:
		return nil, fmt.Errorf("unsupported msgpack type %T", value)
	}
}

func appendMsgpackString(out []byte, value string) []byte {
	size := len(value)
	switch {
	case size <= 31:
		out = append(out, 0xa0|byte(size))
	case size <= maxMsgpackUint8:
		out = append(out, 0xd9, byte(size))
	case size <= maxMsgpackUint16:
		var buf [3]byte
		buf[0] = 0xda
		binary.BigEndian.PutUint16(buf[1:], uint16(size))
		out = append(out, buf[:]...)
	default:
		var buf [5]byte
		buf[0] = 0xdb
		binary.BigEndian.PutUint32(buf[1:], uint32(size))
		out = append(out, buf[:]...)
	}
	return append(out, value...)
}

func appendMsgpackBinary(out []byte, value []byte) []byte {
	size := len(value)
	switch {
	case size <= maxMsgpackUint8:
		out = append(out, 0xc4, byte(size))
	case size <= maxMsgpackUint16:
		var buf [3]byte
		buf[0] = 0xc5
		binary.BigEndian.PutUint16(buf[1:], uint16(size))
		out = append(out, buf[:]...)
	default:
		var buf [5]byte
		buf[0] = 0xc6
		binary.BigEndian.PutUint32(buf[1:], uint32(size))
		out = append(out, buf[:]...)
	}
	return append(out, value...)
}

func appendMsgpackMap(out []byte, value map[string]any) ([]byte, error) {
	size := len(value)
	switch {
	case size <= 15:
		out = append(out, 0x80|byte(size))
	case size <= maxMsgpackUint16:
		var buf [3]byte
		buf[0] = 0xde
		binary.BigEndian.PutUint16(buf[1:], uint16(size))
		out = append(out, buf[:]...)
	default:
		var buf [5]byte
		buf[0] = 0xdf
		binary.BigEndian.PutUint32(buf[1:], uint32(size))
		out = append(out, buf[:]...)
	}
	for key, val := range value {
		var err error
		out = appendMsgpackString(out, key)
		out, err = appendMsgpack(out, val)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func appendMsgpackInt(out []byte, value int64) []byte {
	switch {
	case value >= 0:
		return appendMsgpackUint(out, uint64(value))
	case value >= -32:
		return append(out, byte(int8(value)))
	case value >= minMsgpackInt8:
		return append(out, 0xd0, byte(int8(value)))
	case value >= minMsgpackInt16:
		var buf [3]byte
		buf[0] = 0xd1
		binary.BigEndian.PutUint16(buf[1:], uint16(int16(value)))
		return append(out, buf[:]...)
	case value >= minMsgpackInt32:
		var buf [5]byte
		buf[0] = 0xd2
		binary.BigEndian.PutUint32(buf[1:], uint32(int32(value)))
		return append(out, buf[:]...)
	default:
		var buf [9]byte
		buf[0] = 0xd3
		binary.BigEndian.PutUint64(buf[1:], uint64(value))
		return append(out, buf[:]...)
	}
}

func appendMsgpackUint(out []byte, value uint64) []byte {
	switch {
	case value <= 0x7f:
		return append(out, byte(value))
	case value <= maxMsgpackUint8:
		return append(out, 0xcc, byte(value))
	case value <= maxMsgpackUint16:
		var buf [3]byte
		buf[0] = 0xcd
		binary.BigEndian.PutUint16(buf[1:], uint16(value))
		return append(out, buf[:]...)
	case value <= maxMsgpackUint32:
		var buf [5]byte
		buf[0] = 0xce
		binary.BigEndian.PutUint32(buf[1:], uint32(value))
		return append(out, buf[:]...)
	default:
		var buf [9]byte
		buf[0] = 0xcf
		binary.BigEndian.PutUint64(buf[1:], value)
		return append(out, buf[:]...)
	}
}

func unmarshalMsgpackMap(data []byte) (map[string]any, error) {
	value, offset, err := readMsgpackValue(data, 0)
	if err != nil {
		return nil, err
	}
	if offset != len(data) {
		return nil, fmt.Errorf("msgpack trailing bytes: %d", len(data)-offset)
	}
	out, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("msgpack root type %T, want map", value)
	}
	return out, nil
}

func readMsgpackValue(data []byte, offset int) (any, int, error) {
	if offset >= len(data) {
		return nil, offset, fmt.Errorf("unexpected msgpack eof")
	}
	prefix := data[offset]
	offset++

	switch {
	case prefix <= 0x7f:
		return int64(prefix), offset, nil
	case prefix >= 0x80 && prefix <= 0x8f:
		return readMsgpackMap(data, offset, int(prefix&0x0f))
	case prefix >= 0x90 && prefix <= 0x9f:
		return readMsgpackArrayAsAny(data, offset, int(prefix&0x0f))
	case prefix >= 0xa0 && prefix <= 0xbf:
		return readMsgpackString(data, offset, int(prefix&0x1f))
	case prefix >= 0xe0:
		return int64(int8(prefix)), offset, nil
	}

	switch prefix {
	case 0xc0:
		return nil, offset, nil
	case 0xc2:
		return false, offset, nil
	case 0xc3:
		return true, offset, nil
	case 0xc4:
		size, next, err := readUint(data, offset, 1)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackBinary(data, next, int(size))
	case 0xc5:
		size, next, err := readUint(data, offset, 2)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackBinary(data, next, int(size))
	case 0xc6:
		size, next, err := readUint(data, offset, 4)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackBinary(data, next, int(size))
	case 0xca:
		size, next, err := readUint(data, offset, 4)
		if err != nil {
			return nil, offset, err
		}
		return float64(math.Float32frombits(uint32(size))), next, nil
	case 0xcb:
		size, next, err := readUint(data, offset, 8)
		if err != nil {
			return nil, offset, err
		}
		return math.Float64frombits(size), next, nil
	case 0xcc:
		value, next, err := readUint(data, offset, 1)
		return int64(value), next, err
	case 0xcd:
		value, next, err := readUint(data, offset, 2)
		return int64(value), next, err
	case 0xce:
		value, next, err := readUint(data, offset, 4)
		return int64(value), next, err
	case 0xcf:
		value, next, err := readUint(data, offset, 8)
		return int64(value), next, err
	case 0xd0:
		if offset >= len(data) {
			return nil, offset, fmt.Errorf("unexpected msgpack eof")
		}
		return int64(int8(data[offset])), offset + 1, nil
	case 0xd1:
		value, next, err := readUint(data, offset, 2)
		return int64(int16(value)), next, err
	case 0xd2:
		value, next, err := readUint(data, offset, 4)
		return int64(int32(value)), next, err
	case 0xd3:
		value, next, err := readUint(data, offset, 8)
		return int64(value), next, err
	case 0xd9:
		size, next, err := readUint(data, offset, 1)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackString(data, next, int(size))
	case 0xda:
		size, next, err := readUint(data, offset, 2)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackString(data, next, int(size))
	case 0xdb:
		size, next, err := readUint(data, offset, 4)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackString(data, next, int(size))
	case 0xdc:
		size, next, err := readUint(data, offset, 2)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackArrayAsAny(data, next, int(size))
	case 0xdd:
		size, next, err := readUint(data, offset, 4)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackArrayAsAny(data, next, int(size))
	case 0xde:
		size, next, err := readUint(data, offset, 2)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackMap(data, next, int(size))
	case 0xdf:
		size, next, err := readUint(data, offset, 4)
		if err != nil {
			return nil, offset, err
		}
		return readMsgpackMap(data, next, int(size))
	default:
		return nil, offset, fmt.Errorf("unsupported msgpack prefix 0x%x", prefix)
	}
}

func readMsgpackMap(data []byte, offset, size int) (map[string]any, int, error) {
	out := make(map[string]any, size)
	for i := 0; i < size; i++ {
		keyValue, next, err := readMsgpackValue(data, offset)
		if err != nil {
			return nil, offset, err
		}
		key, ok := keyValue.(string)
		if !ok {
			return nil, offset, fmt.Errorf("msgpack map key type %T, want string", keyValue)
		}
		value, valueNext, err := readMsgpackValue(data, next)
		if err != nil {
			return nil, offset, err
		}
		out[key] = value
		offset = valueNext
	}
	return out, offset, nil
}

func readMsgpackArrayAsAny(data []byte, offset, size int) ([]any, int, error) {
	out := make([]any, 0, size)
	for i := 0; i < size; i++ {
		value, next, err := readMsgpackValue(data, offset)
		if err != nil {
			return nil, offset, err
		}
		out = append(out, value)
		offset = next
	}
	return out, offset, nil
}

func readMsgpackString(data []byte, offset, size int) (string, int, error) {
	if offset+size > len(data) {
		return "", offset, fmt.Errorf("unexpected msgpack eof")
	}
	return string(data[offset : offset+size]), offset + size, nil
}

func readMsgpackBinary(data []byte, offset, size int) ([]byte, int, error) {
	if offset+size > len(data) {
		return nil, offset, fmt.Errorf("unexpected msgpack eof")
	}
	out := make([]byte, size)
	copy(out, data[offset:offset+size])
	return out, offset + size, nil
}

func readUint(data []byte, offset, size int) (uint64, int, error) {
	if offset+size > len(data) {
		return 0, offset, fmt.Errorf("unexpected msgpack eof")
	}
	switch size {
	case 1:
		return uint64(data[offset]), offset + 1, nil
	case 2:
		return uint64(binary.BigEndian.Uint16(data[offset:])), offset + 2, nil
	case 4:
		return uint64(binary.BigEndian.Uint32(data[offset:])), offset + 4, nil
	case 8:
		return binary.BigEndian.Uint64(data[offset:]), offset + 8, nil
	default:
		return 0, offset, fmt.Errorf("unsupported uint size %d", size)
	}
}
