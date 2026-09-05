package probe

import (
	"fmt"
	"strconv"
	"strings"
)

const maxBERLength = 65535

func tlv(tag byte, value []byte) []byte {
	result := []byte{tag}
	result = append(result, encodeLength(len(value))...)
	return append(result, value...)
}

func encodeLength(length int) []byte {
	if length < 128 {
		return []byte{byte(length)}
	}
	var encoded [8]byte
	i := len(encoded)
	for length > 0 {
		i--
		encoded[i] = byte(length)
		length >>= 8
	}
	return append([]byte{0x80 | byte(len(encoded)-i)}, encoded[i:]...)
}

func encodeInteger(value int) []byte {
	if value == 0 {
		return []byte{0x02, 0x01, 0x00}
	}
	var encoded [9]byte
	i := len(encoded)
	for value > 0 {
		i--
		encoded[i] = byte(value)
		value >>= 8
	}
	if encoded[i]&0x80 != 0 {
		i--
		encoded[i] = 0
	}
	return tlv(0x02, encoded[i:])
}

func encodeOID(oid string) ([]byte, error) {
	parts := strings.Split(oid, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("OID %q has fewer than two arcs", oid)
	}
	arcs := make([]uint64, len(parts))
	for i, part := range parts {
		arc, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("OID %q has invalid arc %q", oid, part)
		}
		arcs[i] = arc
	}
	if arcs[0] > 2 || arcs[0] < 2 && arcs[1] > 39 {
		return nil, fmt.Errorf("OID %q has invalid first arcs", oid)
	}
	encoded := encodeBase128(arcs[0]*40 + arcs[1])
	for _, arc := range arcs[2:] {
		encoded = append(encoded, encodeBase128(arc)...)
	}
	return encoded, nil
}

func encodeBase128(value uint64) []byte {
	var buffer [10]byte
	i := len(buffer) - 1
	buffer[i] = byte(value & 0x7f)
	for value >>= 7; value > 0; value >>= 7 {
		i--
		buffer[i] = byte(value&0x7f) | 0x80
	}
	return append([]byte(nil), buffer[i:]...)
}

func decodeOID(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty OID")
	}
	values := make([]uint64, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] == 0x80 {
			return "", fmt.Errorf("non-minimal OID arc encoding")
		}
		var value uint64
		for {
			if i >= len(data) || value > (^uint64(0)>>7) {
				return "", fmt.Errorf("truncated or overflowing OID arc")
			}
			b := data[i]
			i++
			value = value<<7 | uint64(b&0x7f)
			if b&0x80 == 0 {
				break
			}
		}
		values = append(values, value)
	}
	first := uint64(2)
	second := values[0] - 80
	if values[0] < 40 {
		first, second = 0, values[0]
	} else if values[0] < 80 {
		first, second = 1, values[0]-40
	}
	parts := []string{strconv.FormatUint(first, 10), strconv.FormatUint(second, 10)}
	for _, value := range values[1:] {
		parts = append(parts, strconv.FormatUint(value, 10))
	}
	return strings.Join(parts, "."), nil
}

type berReader struct {
	data []byte
	pos  int
}

func newBERReader(data []byte) *berReader { return &berReader{data: data} }
func (r *berReader) empty() bool          { return r.pos == len(r.data) }

func (r *berReader) readAny() (byte, []byte, error) {
	if r.pos >= len(r.data) {
		return 0, nil, fmt.Errorf("missing tag")
	}
	tag := r.data[r.pos]
	r.pos++
	length, err := r.readLength()
	if err != nil {
		return 0, nil, err
	}
	if length > len(r.data)-r.pos {
		return 0, nil, fmt.Errorf("value length %d exceeds remaining input", length)
	}
	value := r.data[r.pos : r.pos+length]
	r.pos += length
	return tag, value, nil
}

func (r *berReader) readValue(expectedTag byte) ([]byte, error) {
	tag, value, err := r.readAny()
	if err != nil {
		return nil, err
	}
	if tag != expectedTag {
		return nil, fmt.Errorf("tag 0x%02x, want 0x%02x", tag, expectedTag)
	}
	return value, nil
}

func (r *berReader) readConstructed(tag byte) (*berReader, error) {
	value, err := r.readValue(tag)
	if err != nil {
		return nil, err
	}
	return newBERReader(value), nil
}

func (r *berReader) readInteger() (int, error) {
	value, err := r.readValue(0x02)
	if err != nil || len(value) == 0 || len(value) > 8 || value[0]&0x80 != 0 {
		return 0, fmt.Errorf("invalid non-negative INTEGER")
	}
	var result uint64
	for _, b := range value {
		result = result<<8 | uint64(b)
	}
	if uint64(int(result)) != result {
		return 0, fmt.Errorf("INTEGER overflows int")
	}
	return int(result), nil
}

func (r *berReader) readLength() (int, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("missing length")
	}
	first := r.data[r.pos]
	r.pos++
	if first&0x80 == 0 {
		return int(first), nil
	}
	count := int(first & 0x7f)
	if count == 0 || count > 4 || count > len(r.data)-r.pos {
		return 0, fmt.Errorf("invalid long-form length")
	}
	var length uint64
	for i := 0; i < count; i++ {
		length = length<<8 | uint64(r.data[r.pos])
		r.pos++
	}
	if length > maxBERLength {
		return 0, fmt.Errorf("length %d exceeds maximum %d", length, maxBERLength)
	}
	return int(length), nil
}
