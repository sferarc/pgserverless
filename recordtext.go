package pgserverless

import (
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// oidRecordArray is record[], what array[row(...), ...] returns.
const oidRecordArray uint32 = 2287

// recordColumnValue renders a binary record or record[] column as the PostgreSQL text Neon returns, or reports false.
func recordColumnValue(m *pgtype.Map, raw []byte, typeOID uint32, format int16) (any, bool) {
	if raw == nil || format != pgBinaryFormat || m == nil {
		return nil, false
	}
	switch typeOID {
	case oidRecord:
		s, ok := recordText(m, raw)
		return s, ok
	case oidRecordArray:
		return recordArrayValue(m, raw)
	}
	return nil, false
}

// recordText renders a binary record as PostgreSQL's record_out text.
func recordText(m *pgtype.Map, raw []byte) (string, bool) {
	sc := pgtype.NewCompositeBinaryScanner(m, raw)
	var b strings.Builder
	b.WriteByte('(')
	for i := 0; sc.Next(); i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		field := sc.Bytes()
		if field == nil {
			continue
		}
		s, ok := recordFieldText(m, sc.OID(), field)
		if !ok {
			return "", false
		}
		writeRecordField(&b, s)
	}
	if sc.Err() != nil {
		return "", false
	}
	b.WriteByte(')')
	return b.String(), true
}

// recordFieldText renders one non-NULL binary field as PostgreSQL text.
func recordFieldText(m *pgtype.Map, oid uint32, field []byte) (string, bool) {
	if oid == oidNumeric {
		if s, ok := numericZeroText(field); ok {
			return s, true
		}
	}
	switch oid {
	case oidRecord:
		return recordText(m, field)
	case oidJSON:
		// Binary json is the document as sent; a decode and encode would respace it.
		return string(field), true
	case oidJSONB:
		// Binary jsonb is a version byte, then PostgreSQL's own text.
		if len(field) == 0 || field[0] != 1 {
			return "", false
		}
		return string(field[1:]), true
	}
	t, ok := m.TypeForOID(oid)
	if !ok {
		return "", false
	}
	if codec, isArray := t.Codec.(*pgtype.ArrayCodec); isArray {
		if codec.ElementType == nil {
			return "", false
		}
		return arrayFieldText(m, codec.ElementType.OID, field)
	}
	v, err := t.Codec.DecodeValue(m, oid, pgBinaryFormat, field)
	if err != nil || v == nil {
		return "", false
	}
	switch f := v.(type) {
	case float64:
		return pgFloatText(f, 64), true
	case float32:
		return pgFloatText(float64(f), 32), true
	}
	if oid == oidTimestamp || oid == oidTimestamptz {
		return timestampFieldText(m, oid, v)
	}
	if s, ok := coerceNeonValue(m, v, oid).(string); ok {
		return s, true
	}
	return encodeText(m, oid, v)
}

// pgFloatText spells a float as float8out and float4out do: exponent form past DBL_DIG or FLT_DIG, where pgx always prints digits.
func pgFloatText(f float64, bitSize int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	maxExp := 15
	if bitSize == 32 {
		maxExp = 6
	}
	e := strconv.FormatFloat(f, 'e', -1, bitSize)
	exp, err := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if err == nil && (exp < -4 || exp >= maxExp) {
		return e
	}
	return strconv.FormatFloat(f, 'f', -1, bitSize)
}

// timestampFieldText spells a timestamp as timestamp_out does in a UTC session, where the column path keeps RFC 3339.
func timestampFieldText(m *pgtype.Map, oid uint32, v any) (string, bool) {
	if s, ok := infinityText(v); ok {
		return s, true
	}
	s, ok := encodeText(m, oid, v)
	if ok && oid == oidTimestamptz {
		s = utcOffsetText(s)
	}
	return s, ok
}

// arrayFieldText renders a binary array as array_out does, spelling each element as recordFieldText does.
func arrayFieldText(m *pgtype.Map, elemOID uint32, raw []byte) (string, bool) {
	const headerLen = 12
	const dimLen = 8
	if len(raw) < headerLen {
		return "", false
	}
	ndim := int(int32(binary.BigEndian.Uint32(raw)))
	if ndim == 0 {
		return "{}", true
	}
	if ndim < 0 || ndim > pgMaxArrayDims || len(raw) < headerLen+ndim*dimLen ||
		binary.BigEndian.Uint32(raw[8:]) != elemOID {
		return "", false
	}
	dims := make([]int, ndim)
	var b strings.Builder
	total := 1
	decorated := false
	for i := range dims {
		d := raw[headerLen+i*dimLen:]
		n := int(int32(binary.BigEndian.Uint32(d)))
		if n <= 0 || total > math.MaxInt32/n {
			return "", false
		}
		dims[i] = n
		total *= n
		if int32(binary.BigEndian.Uint32(d[4:])) != 1 {
			decorated = true
		}
	}
	if decorated {
		// array_out writes the bounds only when some lower bound is not 1.
		for i := range dims {
			lb := int64(int32(binary.BigEndian.Uint32(raw[headerLen+i*dimLen+4:])))
			b.WriteByte('[')
			b.WriteString(strconv.FormatInt(lb, 10))
			b.WriteByte(':')
			b.WriteString(strconv.FormatInt(lb+int64(dims[i])-1, 10))
			b.WriteByte(']')
		}
		b.WriteByte('=')
	}
	delim := byte(',')
	if elemOID == oidBox {
		delim = ';'
	}
	rest := raw[headerLen+ndim*dimLen:]
	for i := range total {
		// Open one brace per dimension this element starts, close one per dimension it ends.
		stride := total
		for _, d := range dims {
			if i%stride == 0 {
				b.WriteByte('{')
			}
			stride /= d
		}
		if len(rest) < 4 {
			return "", false
		}
		n := int(int32(binary.BigEndian.Uint32(rest)))
		rest = rest[4:]
		if n < 0 {
			b.WriteString("NULL")
		} else {
			if n > len(rest) {
				return "", false
			}
			s, ok := recordFieldText(m, elemOID, rest[:n])
			if !ok {
				return "", false
			}
			writeArrayElem(&b, s, delim)
			rest = rest[n:]
		}
		stride = 1
		for _, dim := range slices.Backward(dims) {
			stride *= dim
			if (i+1)%stride != 0 {
				break
			}
			b.WriteByte('}')
		}
		if i < total-1 {
			b.WriteByte(delim)
		}
	}
	if len(rest) != 0 {
		return "", false
	}
	return b.String(), true
}

// writeArrayElem appends s quoted as array_out quotes an element.
func writeArrayElem(b *strings.Builder, s string, delim byte) {
	quote := s == "" || strings.EqualFold(s, "NULL")
	for i := 0; !quote && i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\', '{', '}', delim, ' ', '\t', '\n', '\r', '\v', '\f':
			quote = true
		}
	}
	if !quote {
		b.WriteString(s)
		return
	}
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
}

func encodeText(m *pgtype.Map, oid uint32, v any) (string, bool) {
	buf, err := m.Encode(oid, pgtype.TextFormatCode, v, nil)
	if err != nil || buf == nil {
		return "", false
	}
	return string(buf), true
}

// writeRecordField appends s quoted as record_out quotes a field.
func writeRecordField(b *strings.Builder, s string) {
	if s != "" && !strings.ContainsAny(s, "\"\\(), \t\n\r\v\f") {
		b.WriteString(s)
		return
	}
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte(s[i])
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
}

// recordArrayValue renders a binary record[] as nested record text strings and nulls.
func recordArrayValue(m *pgtype.Map, raw []byte) (any, bool) {
	const headerLen = 12
	const dimLen = 8
	if len(raw) < headerLen {
		return nil, false
	}
	ndim := int(int32(binary.BigEndian.Uint32(raw)))
	if ndim == 0 {
		return []any{}, true
	}
	if ndim < 0 || ndim > pgMaxArrayDims || len(raw) < headerLen+ndim*dimLen ||
		binary.BigEndian.Uint32(raw[8:]) != oidRecord {
		return nil, false
	}
	dims := make([]int, ndim)
	for i := range dims {
		dims[i] = int(int32(binary.BigEndian.Uint32(raw[headerLen+i*dimLen:])))
	}
	rest := raw[headerLen+ndim*dimLen:]
	var flat []any
	for len(rest) > 0 {
		if len(rest) < 4 {
			return nil, false
		}
		n := int(int32(binary.BigEndian.Uint32(rest)))
		rest = rest[4:]
		if n < 0 {
			flat = append(flat, nil)
			continue
		}
		if n > len(rest) {
			return nil, false
		}
		s, ok := recordText(m, rest[:n])
		if !ok {
			return nil, false
		}
		flat = append(flat, s)
		rest = rest[n:]
	}
	if ndim == 1 {
		if len(flat) != dims[0] {
			return nil, false
		}
		return flat, true
	}
	nested, ok := nestFlatArray(flat, dims)
	if !ok {
		return nil, false
	}
	return nested, true
}
