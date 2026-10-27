package pgserverless

import (
	"encoding/binary"
	"strings"
)

// pgNumericDscaleMax is NUMERIC_DSCALE_MAX, the largest display scale PostgreSQL stores.
const pgNumericDscaleMax = 0x3FFF

// numericScaleValue restores the scale pgx drops from a binary numeric zero, so 0.00 reads "0.00" as PostgreSQL prints it, not "0".
func numericScaleValue(v any, raw []byte, typeOID uint32, format int16) any {
	if raw == nil || format != pgBinaryFormat {
		return v
	}
	switch typeOID {
	case oidNumeric:
		if s, ok := numericZeroText(raw); ok {
			return s
		}
	case oidNumericArr:
		flat, ok := v.([]any)
		if !ok {
			return v
		}
		elems, ok := binaryArrayElems(raw, oidNumeric)
		if !ok || len(elems) != len(flat) {
			return v
		}
		for i, e := range elems {
			if s, ok := numericZeroText(e); ok {
				flat[i] = s
			}
		}
	}
	return v
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
