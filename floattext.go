package pgserverless

import (
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgTextFloatCodec has PostgreSQL send float4 and float8 as text, spelled in the
// session extra_float_digits, and parses that text as a float64 as Neon's
// pg_text_to_json does. extra_float_digits is not reported in ParameterStatus,
// so a binary float always came back with its shortest round-trip digits where
// Neon returns 0.3 for 0.1::float8 + 0.2 under extra_float_digits = 0.
// Parameters still encode as the embedded codec does.
type pgTextFloatCodec struct {
	pgtype.Codec
}

func (pgTextFloatCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextFloatCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		if f, err := strconv.ParseFloat(string(src), 64); err == nil {
			return f, nil
		}
		return string(src), nil
	}
	return c.Codec.DecodeValue(m, oid, format, src)
}

// pgTextFloatArrayCodec does the same for float4[] and float8[], whose literal textFloatArrayElems splits.
type pgTextFloatArrayCodec struct {
	*pgtype.ArrayCodec
}

func (pgTextFloatArrayCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextFloatArrayCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.ArrayCodec.DecodeValue(m, oid, format, src)
}

// useTextFloat makes m ask for float4, float8 and their arrays as text. It must
// run before the query is sent, since the result formats go out with it.
func useTextFloat(m *pgtype.Map) {
	if m == nil {
		return
	}
	if t, ok := m.TypeForOID(pgtype.Float8OID); ok {
		if _, done := t.Codec.(pgTextFloatCodec); done {
			return
		}
	}
	float4 := &pgtype.Type{Name: "float4", OID: pgtype.Float4OID, Codec: pgTextFloatCodec{pgtype.Float4Codec{}}}
	float8 := &pgtype.Type{Name: "float8", OID: pgtype.Float8OID, Codec: pgTextFloatCodec{pgtype.Float8Codec{}}}
	m.RegisterTypes([]*pgtype.Type{
		float4,
		float8,
		{Name: "_float4", OID: pgtype.Float4ArrayOID, Codec: pgTextFloatArrayCodec{&pgtype.ArrayCodec{ElementType: float4}}},
		{Name: "_float8", OID: pgtype.Float8ArrayOID, Codec: pgTextFloatArrayCodec{&pgtype.ArrayCodec{ElementType: float8}}},
	})
}

// textFloatArrayElems splits a float4[] or float8[] literal into float64
// elements at every depth, spelling NaN and the infinities as PostgreSQL does.
func textFloatArrayElems(v any) ([]any, bool) {
	elems, ok := textArrayElems(v, oidFloat8)
	if !ok {
		return nil, false
	}
	return nonFiniteFloatElems(elems), true
}

func nonFiniteFloatElems(elems []any) []any {
	for i, e := range elems {
		if sub, ok := e.([]any); ok {
			elems[i] = nonFiniteFloatElems(sub)
		} else if s, ok := nonFiniteFloatString(e); ok {
			elems[i] = s
		}
	}
	return elems
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
