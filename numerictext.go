package pgserverless

import (
	"encoding/binary"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgNumericDscaleMax is NUMERIC_DSCALE_MAX, the largest display scale PostgreSQL stores.
const pgNumericDscaleMax = 0x3FFF

// numericScaleValue restores the scale pgx drops from a binary numeric zero, so 0.00 reads "0.00" as PostgreSQL prints it, not "0".
func numericScaleValue(m *pgtype.Map, v any, raw []byte, typeOID uint32, format int16) any {
	if raw == nil || format != pgBinaryFormat {
		return v
	}
	if s, ok := numericScaleText(m, raw, typeOID); ok {
		return s
	}
	var elemOID uint32
	switch typeOID {
	case oidNumericArr:
		elemOID = oidNumeric
	case pgtype.NumrangeArrayOID:
		elemOID = pgtype.NumrangeOID
	default:
		return v
	}
	flat, ok := v.([]any)
	if !ok {
		return v
	}
	elems, ok := binaryArrayElems(raw, elemOID)
	if !ok || len(elems) != len(flat) {
		return v
	}
	for i, e := range elems {
		if s, ok := numericScaleText(m, e, elemOID); ok {
			flat[i] = s
		}
	}
	return v
}

// numericScaleText renders a binary numeric, numrange or nummultirange as PostgreSQL prints it when it holds a scaled zero, or reports false.
func numericScaleText(m *pgtype.Map, raw []byte, typeOID uint32) (string, bool) {
	switch typeOID {
	case oidNumeric:
		return numericZeroText(raw)
	case pgtype.NumrangeOID:
		if s, scaled, ok := numrangeText(m, raw); ok && scaled {
			return s, true
		}
	case pgtype.NummultirangeOID:
		return nummultirangeZeroText(m, raw)
	}
	return "", false
}

// The flag bits of range_send's leading byte.
const (
	rangeFlagEmpty          = 0x01
	rangeFlagLowerInclusive = 0x02
	rangeFlagUpperInclusive = 0x04
	rangeFlagLowerInfinite  = 0x08
	rangeFlagUpperInfinite  = 0x10
)

// numrangeText renders a binary numrange as range_out does and reports whether a bound was a scaled zero.
func numrangeText(m *pgtype.Map, raw []byte) (s string, scaled, ok bool) {
	if len(raw) == 0 {
		return "", false, false
	}
	flags := raw[0]
	if flags&rangeFlagEmpty != 0 {
		return "empty", false, len(raw) == 1
	}
	rest := raw[1:]
	var b strings.Builder
	if flags&rangeFlagLowerInclusive != 0 {
		b.WriteByte('[')
	} else {
		b.WriteByte('(')
	}
	for i, infinite := range []byte{rangeFlagLowerInfinite, rangeFlagUpperInfinite} {
		if i > 0 {
			b.WriteByte(',')
		}
		if flags&infinite != 0 {
			continue
		}
		if len(rest) < 4 {
			return "", false, false
		}
		n := int(int32(binary.BigEndian.Uint32(rest)))
		rest = rest[4:]
		if n < 0 || n > len(rest) {
			return "", false, false
		}
		bound, zero := numericZeroText(rest[:n])
		if !zero {
			var ok bool
			if bound, ok = numericBinaryText(m, rest[:n]); !ok {
				return "", false, false
			}
		}
		scaled = scaled || zero
		b.WriteString(bound)
		rest = rest[n:]
	}
	if len(rest) != 0 {
		return "", false, false
	}
	if flags&rangeFlagUpperInclusive != 0 {
		b.WriteByte(']')
	} else {
		b.WriteByte(')')
	}
	return b.String(), scaled, true
}

// nummultirangeZeroText renders a binary nummultirange as multirange_out does when one of its bounds is a scaled zero, or reports false.
func nummultirangeZeroText(m *pgtype.Map, raw []byte) (string, bool) {
	if len(raw) < 4 {
		return "", false
	}
	count := binary.BigEndian.Uint32(raw)
	rest := raw[4:]
	var b strings.Builder
	b.WriteByte('{')
	anyScaled := false
	for i := range count {
		if len(rest) < 4 {
			return "", false
		}
		n := int(int32(binary.BigEndian.Uint32(rest)))
		rest = rest[4:]
		if n < 0 || n > len(rest) {
			return "", false
		}
		s, scaled, ok := numrangeText(m, rest[:n])
		if !ok {
			return "", false
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s)
		anyScaled = anyScaled || scaled
		rest = rest[n:]
	}
	if !anyScaled || len(rest) != 0 {
		return "", false
	}
	b.WriteByte('}')
	return b.String(), true
}

// numericBinaryText renders a binary numeric that is not a scaled zero through pgx, which keeps the scale of every value with digits.
func numericBinaryText(m *pgtype.Map, raw []byte) (string, bool) {
	if m == nil {
		return "", false
	}
	t, ok := m.TypeForOID(oidNumeric)
	if !ok {
		return "", false
	}
	v, err := t.Codec.DecodeValue(m, oidNumeric, pgBinaryFormat, raw)
	if err != nil {
		return "", false
	}
	return numericString(v)
}

// numericZeroText renders PostgreSQL's binary zero (no digits) with its display scale, or reports false for anything else.
func numericZeroText(raw []byte) (string, bool) {
	if len(raw) != 8 || binary.BigEndian.Uint16(raw) != 0 || binary.BigEndian.Uint16(raw[4:]) != 0 {
		return "", false
	}
	dscale := int(int16(binary.BigEndian.Uint16(raw[6:])))
	if dscale <= 0 || dscale > pgNumericDscaleMax {
		return "", false
	}
	return "0." + strings.Repeat("0", dscale), true
}

// binaryArrayElems splits a binary array of elemOID into its elements in row-major order, nil for NULL, or reports false.
func binaryArrayElems(raw []byte, elemOID uint32) ([][]byte, bool) {
	const headerLen = 12
	const dimLen = 8
	if len(raw) < headerLen {
		return nil, false
	}
	ndim := int(int32(binary.BigEndian.Uint32(raw)))
	if ndim == 0 {
		return [][]byte{}, true
	}
	if ndim < 0 || ndim > pgMaxArrayDims || len(raw) < headerLen+ndim*dimLen ||
		binary.BigEndian.Uint32(raw[8:]) != elemOID {
		return nil, false
	}
	rest := raw[headerLen+ndim*dimLen:]
	var elems [][]byte
	for len(rest) > 0 {
		if len(rest) < 4 {
			return nil, false
		}
		n := int(int32(binary.BigEndian.Uint32(rest)))
		rest = rest[4:]
		if n < 0 {
			elems = append(elems, nil)
			continue
		}
		if n > len(rest) {
			return nil, false
		}
		elems = append(elems, rest[:n])
		rest = rest[n:]
	}
	return elems, true
}
