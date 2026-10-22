package pgserverless

import (
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArrayElemOID(t *testing.T) {
	for arr, want := range map[uint32]uint32{
		oidBoolArray:   oidBool,
		oidInt2Array:   oidInt2,
		oidInt4Array:   oidInt4,
		oidInt8Array:   oidInt8,
		oidFloat4Array: oidFloat4,
		oidFloat8Array: oidFloat8,
		oidTextArray:   25,
		oidVarcharArr:  25,
		oidJSONArray:   oidJSON,
		oidJSONBArray:  oidJSONB,
		0:              25,
	} {
		assert.Equal(t, want, arrayElemOID(arr), "array OID %d", arr)
	}
}

func TestConvertTextValueArrays(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		oid  uint32
		want any
	}{
		{"bool", "{t,f,NULL}", oidBoolArray, []any{true, false, nil}},
		{"int2", "{1,-2}", oidInt2Array, []any{1, -2}},
		{"int8 stays text", "{9223372036854775807}", oidInt8Array, []any{"9223372036854775807"}},
		{"float8", "{1.5,2}", oidFloat8Array, []any{1.5, 2.0}},
		{"text with comma", `{"a,b","c"}`, oidTextArray, []any{"a,b", "c"}},
		{"json", `{"{\"k\":1}"}`, oidJSONBArray, []any{map[string]any{"k": float64(1)}}},
		{"not a literal falls back to the text", "plain", oidInt4Array, "plain"},
		{"unparsable int stays text", "{x}", oidInt4Array, []any{"x"}},
		{"unparsable json stays text", "{nope}", oidJSONArray, []any{"nope"}},
		{"unparsable bool is false", "{maybe}", oidBoolArray, []any{false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, convertTextValue([]byte(tc.raw), tc.oid))
		})
	}
}

func TestFormatPgArrayElem(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "NULL"},
		{"whole float", float64(1000000), "1000000"},
		{"fraction", 0.25, "0.25"},
		{"float32", float32(0.5), "0.5"},
		{"NaN", math.NaN(), `"NaN"`},
		{"+Inf", math.Inf(1), `"Infinity"`},
		{"-Inf", math.Inf(-1), `"-Infinity"`},
		{"nested", []any{1.0, []any{"a b"}}, `{1,{"a b"}}`},
		{"object", map[string]any{"k": "v"}, `"{\"k\":\"v\"}"`},
		{"int", 7, "7"},
		{"bool", true, "true"},
		{"keyword", "Null", `"Null"`},
		{"empty", "", `""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatPgArrayElem(tc.in))
		})
	}
}

func TestTextArrayElems(t *testing.T) {
	got, ok := textArrayElems(`{$1.00,"$1,000.50",NULL}`, oidMoney)
	require.True(t, ok)
	assert.Equal(t, []any{"$1.00", "$1,000.50", nil}, got)

	_, ok = textArrayElems("not an array", oidMoney)
	assert.False(t, ok, "text that is not an array literal is left for the caller")
	_, ok = textArrayElems(42, oidMoney)
	assert.False(t, ok)
}

func TestRangeOIDLookupsRejectUnrelatedTypes(t *testing.T) {
	m := pgtype.NewMap()

	_, ok := multirangeRangeOID(m, 0)
	assert.False(t, ok, "unknown OID")
	_, ok = multirangeRangeOID(m, oidInt4)
	assert.False(t, ok, "a scalar type is not a multirange")
	_, ok = rangeElemOID(m, oidInt4)
	assert.False(t, ok, "a scalar type is not a range")

	elem, ok := rangeElemOID(m, pgtype.Int4rangeOID)
	require.True(t, ok)
	assert.Equal(t, oidInt4, elem)
	rng, ok := multirangeRangeOID(m, pgtype.Int4multirangeOID)
	require.True(t, ok)
	assert.Equal(t, pgtype.Int4rangeOID, int(rng))
}

func TestNeonArrayElemOIDMapsEveryListedArray(t *testing.T) {
	for arr, want := range map[uint32]uint32{
		oidFloat4Array:      oidFloat4,
		oidFloat8Array:      oidFloat8,
		oidTimestampArray:   oidTimestamp,
		oidTimestamptzArray: oidTimestamptz,
		oidLsegArray:        oidLseg,
		oidPathArray:        oidPath,
		oidBoxArray:         oidBox,
		oidPolygonArray:     oidPolygon,
		oidLineArray:        oidLine,
		oidCircleArray:      oidCircle,
		oidTSVectorArray:    oidTSVector,
		oidInetArray:        oidInet,
	} {
		got, ok := neonArrayElemOID(arr)
		assert.True(t, ok, "array OID %d", arr)
		assert.Equal(t, want, got, "array OID %d", arr)
	}
	_, ok := neonArrayElemOID(oidInt4Array)
	assert.False(t, ok, "int4[] needs no coercion")
}

func TestConvertParamsLeavesScalarsAndEmptyAlone(t *testing.T) {
	assert.Nil(t, ConvertParams(nil))
	assert.Equal(t, []any{}, ConvertParams([]any{}))

	got := ConvertParams([]any{"s", 1.5, nil, true, []any{"a"}, map[string]any{"k": 1.0}})
	assert.Equal(t, []any{"s", 1.5, nil, true, `{a}`, `{"k":1}`}, got)
}
