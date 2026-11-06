package pgserverless

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

type recordField struct {
	oid  uint32
	data []byte // nil is SQL NULL
}

// binaryRecord lays fields out as PostgreSQL's record_send does.
func binaryRecord(fields ...recordField) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(fields)))
	for _, f := range fields {
		b = binary.BigEndian.AppendUint32(b, f.oid)
		if f.data == nil {
			b = binary.BigEndian.AppendUint32(b, 0xffffffff)
			continue
		}
		b = binary.BigEndian.AppendUint32(b, uint32(len(f.data)))
		b = append(b, f.data...)
	}
	return b
}

// binaryRecordArray lays records out as a record[] with the given dimensions.
func binaryRecordArray(dims []int, elems ...[]byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(dims)))
	b = binary.BigEndian.AppendUint32(b, 0)
	b = binary.BigEndian.AppendUint32(b, oidRecord)
	for _, d := range dims {
		b = binary.BigEndian.AppendUint32(b, uint32(d))
		b = binary.BigEndian.AppendUint32(b, 1)
	}
	for _, e := range elems {
		if e == nil {
			b = binary.BigEndian.AppendUint32(b, 0xffffffff)
			continue
		}
		b = binary.BigEndian.AppendUint32(b, uint32(len(e)))
		b = append(b, e...)
	}
	return b
}

func encodeBinary(t *testing.T, m *pgtype.Map, oid uint32, v any) recordField {
	t.Helper()
	b, err := m.Encode(oid, pgtype.BinaryFormatCode, v, nil)
	require.NoError(t, err)
	if b == nil {
		b = []byte{}
	}
	return recordField{oid: oid, data: b}
}

func TestRecordText(t *testing.T) {
	m := pgtype.NewMap()
	int4 := func(n int32) recordField { return encodeBinary(t, m, oidInt4, n) }
	text := func(s string) recordField { return encodeBinary(t, m, 25, s) }

	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{"captured row(bytea, int4)", []byte(rawRecordByteaInt4), `("\\x000000020000000000000001",7)`},
		{"plain fields", binaryRecord(int4(1), text("a")), `(1,a)`},
		{"no fields", binaryRecord(), `()`},
		{"NULL is an empty field", binaryRecord(int4(1), recordField{oid: oidInt4}), `(1,)`},
		{"empty string is quoted", binaryRecord(text("")), `("")`},
		{"whitespace is quoted", binaryRecord(text("a b")), `("a b")`},
		{"quote and backslash are doubled", binaryRecord(text(`x"y\z`)), `("x""y\\z")`},
		{"comma and parentheses are quoted", binaryRecord(text("(a,b)")), `("(a,b)")`},
		{"nested record is quoted", binaryRecord(recordField{oid: oidRecord, data: binaryRecord(int4(1), int4(2))}, int4(3)), `("(1,2)",3)`},
		{"bool", binaryRecord(encodeBinary(t, m, oidBool, true)), `(t)`},
		{"float8 below DBL_DIG digits", binaryRecord(encodeBinary(t, m, oidFloat8, 123456789012345.5)), `(123456789012345.5)`},
		{"float8 large is exponent form", binaryRecord(encodeBinary(t, m, oidFloat8, 1e20)), `(1e+20)`},
		{"float8 small is exponent form", binaryRecord(encodeBinary(t, m, oidFloat8, 1.5e-5)), `(1.5e-05)`},
		{"float8 infinity", binaryRecord(encodeBinary(t, m, oidFloat8, math.Inf(-1))), `(-Infinity)`},
		{"float4 past FLT_DIG is exponent form", binaryRecord(encodeBinary(t, m, oidFloat4, float32(1234567))), `(1.234567e+06)`},
		{"float4 short", binaryRecord(encodeBinary(t, m, oidFloat4, float32(0.1))), `(0.1)`},
		{"int8 stays exact", binaryRecord(encodeBinary(t, m, oidInt8, int64(9007199254740993))), `(9007199254740993)`},
		{"json is the document as sent", binaryRecord(recordField{oid: oidJSON, data: []byte(`{"b":1, "a":2}`)}), `("{""b"":1, ""a"":2}")`},
		{"jsonb drops its version byte", binaryRecord(recordField{oid: oidJSONB, data: []byte("\x01{\"a\": 1}")}), `("{""a"": 1}")`},
		{"two-dimensional array keeps its dimensions", binaryRecord(recordField{oid: oidInt4Array, data: []byte(rawInt4Dim2)}), `("{{1,2},{3,4}}")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := recordText(m, tt.raw)
			require.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("a field type the map cannot render leaves the column alone", func(t *testing.T) {
		_, ok := recordText(m, binaryRecord(int4(1), recordField{oid: 999999, data: []byte{1}}))
		assert.False(t, ok)
	})

	t.Run("truncated bytes leave the column alone", func(t *testing.T) {
		_, ok := recordText(m, []byte(rawRecordByteaInt4)[:10])
		assert.False(t, ok)
	})
}

// Captured from PostgreSQL 17.11 in a UTC session: each raw value is the binary
// record the JSON path reads, and each want is the same expression cast to text.
var recordFieldsFromPostgres = []struct {
	sql  string
	raw  string
	want string
}{
	{
		`row('2024-01-02 03:04:05+00'::timestamptz, '10000-01-01'::timestamp, '0044-03-15 BC'::timestamptz)`,
		"\x00\x00\x00\x03\x00\x00\x04\xa0\x00\x00\x00\b\x00\x02\xb0\xec\x85\x15\xf3@\x00\x00\x04Z\x00\x00\x00\b\x03\x80\xe7\v\x91;\x80\x00\x00\x00\x04\xa0\x00\x00\x00\b\xff\x1a\xf9\xde\xec[ \x00",
		`("2024-01-02 03:04:05+00","10000-01-01 00:00:00","0044-03-15 00:00:00+00 BC")`,
	},
	{
		`row(array['01:00'::time], array['1 day'::interval], array[1e20::float8, 0.5], array[0.00::numeric])`,
		"\x00\x00\x00\x04\x00\x00\x04\x9f\x00\x00\x00 \x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x04;\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\b\x00\x00\x00\x00\u0593\xa4\x00\x00\x00\x04\xa3\x00\x00\x00(\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x04\xa2\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x10\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x03\xfe\x00\x00\x00,\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x02\xbd\x00\x00\x00\x02\x00\x00\x00\x01\x00\x00\x00\bD\x15\xaf\x1dx\xb5\x8c@\x00\x00\x00\b?\xe0\x00\x00\x00\x00\x00\x00\x00\x00\x04\xcf\x00\x00\x00 \x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x06\xa4\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\b\x00\x00\x00\x00\x00\x00\x00\x02",
		`({01:00:00},"{""1 day""}","{1e+20,0.5}",{0.00})`,
	},
	{
		`row(array[box '((1,1),(0,0))', box '((2,2),(1,1))'], '[0:1]={1,2}'::int4[], array['{"b":1, "a":2}'::json], array['a b','','NULL','x"y'])`,
		"\x00\x00\x00\x04\x00\x00\x03\xfc\x00\x00\x00\\\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x02[\x00\x00\x00\x02\x00\x00\x00\x01\x00\x00\x00 ?\xf0\x00\x00\x00\x00\x00\x00?\xf0\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00 @\x00\x00\x00\x00\x00\x00\x00@\x00\x00\x00\x00\x00\x00\x00?\xf0\x00\x00\x00\x00\x00\x00?\xf0\x00\x00\x00\x00\x00\x00\x00\x00\x03\xef\x00\x00\x00$\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x17\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x04\x00\x00\x00\x01\x00\x00\x00\x04\x00\x00\x00\x02\x00\x00\x00\xc7\x00\x00\x00&\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00r\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x0e{\"b\":1, \"a\":2}\x00\x00\x03\xf1\x00\x00\x00.\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x19\x00\x00\x00\x04\x00\x00\x00\x01\x00\x00\x00\x03a b\x00\x00\x00\x00\x00\x00\x00\x04NULL\x00\x00\x00\x03x\"y",
		`("{(1,1),(0,0);(2,2),(1,1)}","[0:1]={1,2}","{""{\\""b\\"":1, \\""a\\"":2}""}","{""a b"","""",""NULL"",""x\\""y""}")`,
	},
	{
		`row(array['192.168.1.1'::inet], array['8 days 01:02:03'::interval], array[row(1,'a b')], '[-2:-1][3:4]={{1,2},{3,NULL}}'::int8[])`,
		"\x00\x00\x00\x04\x00\x00\x04\x11\x00\x00\x00 \x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x03e\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\b\x02 \x00\x04\xc0\xa8\x01\x01\x00\x00\x04\xa3\x00\x00\x00(\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x04\xa2\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x10\x00\x00\x00\x00\xdd\xe8x\xc0\x00\x00\x00\b\x00\x00\x00\x00\x00\x00\b\xef\x00\x00\x003\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\b\xc9\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00\x1b\x00\x00\x00\x02\x00\x00\x00\x17\x00\x00\x00\x04\x00\x00\x00\x01\x00\x00\x02\xc1\x00\x00\x00\x03a b\x00\x00\x03\xf8\x00\x00\x00D\x00\x00\x00\x02\x00\x00\x00\x01\x00\x00\x00\x14\x00\x00\x00\x02\xff\xff\xff\xfe\x00\x00\x00\x02\x00\x00\x00\x03\x00\x00\x00\b\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\b\x00\x00\x00\x00\x00\x00\x00\x02\x00\x00\x00\b\x00\x00\x00\x00\x00\x00\x00\x03\xff\xff\xff\xff",
		`({192.168.1.1},"{""8 days 01:02:03""}","{""(1,\\""a b\\"")""}","[-2:-1][3:4]={{1,2},{3,NULL}}")`,
	},
}

// Fields are spelled as record_out spells them: timestamps as timestamp_out, and
// arrays as array_out with each element in its own type's PostgreSQL text.
func TestRecordTextMatchesPostgres(t *testing.T) {
	m := pgtype.NewMap()
	for _, tt := range recordFieldsFromPostgres {
		t.Run(tt.sql, func(t *testing.T) {
			got, ok := recordText(m, []byte(tt.raw))
			require.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestArrayFieldTextRejectsMalformedArrays(t *testing.T) {
	m := pgtype.NewMap()
	valid := []byte(rawInt4Dim2)
	got, ok := arrayFieldText(m, oidInt4, valid)
	require.True(t, ok)
	require.Equal(t, "{{1,2},{3,4}}", got)

	for name, raw := range map[string][]byte{
		"truncated element": valid[:len(valid)-1],
		"trailing bytes":    append(append([]byte{}, valid...), 0),
		"short header":      valid[:8],
		"element OID differs": func() []byte {
			b := append([]byte{}, valid...)
			binary.BigEndian.PutUint32(b[8:], oidInt8)
			return b
		}(),
		"zero length dimension": func() []byte {
			b := append([]byte{}, valid...)
			binary.BigEndian.PutUint32(b[12:], 0)
			return b
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := arrayFieldText(m, oidInt4, raw)
			assert.False(t, ok)
		})
	}
}

func TestWriteArrayElemMatchesArrayOut(t *testing.T) {
	for _, tt := range []struct {
		in    string
		delim byte
		want  string
	}{
		{"abc", ',', `abc`},
		{"", ',', `""`},
		{"null", ',', `"null"`},
		{"a,b", ',', `"a,b"`},
		{"a;b", ',', `a;b`},
		{"(1,1),(0,0)", ';', `(1,1),(0,0)`},
		{"a;b", ';', `"a;b"`},
		{"a\vb", ',', "\"a\vb\""},
		{`x"y\z`, ',', `"x\"y\\z"`},
		{"{x}", ',', `"{x}"`},
		{"(x)", ',', `(x)`},
	} {
		var b strings.Builder
		writeArrayElem(&b, tt.in, tt.delim)
		assert.Equal(t, tt.want, b.String(), "input %q", tt.in)
	}
}

func TestRecordColumnValue(t *testing.T) {
	m := pgtype.NewMap()
	one := binaryRecord(recordField{oid: oidInt4, data: []byte{0, 0, 0, 1}})
	two := binaryRecord(recordField{oid: oidInt4, data: []byte{0, 0, 0, 2}})

	t.Run("record[] is an array of record text", func(t *testing.T) {
		got, ok := recordColumnValue(m, binaryRecordArray([]int{3}, one, nil, two), oidRecordArray, pgBinaryFormat)
		require.True(t, ok)
		assert.Equal(t, []any{"(1)", nil, "(2)"}, got)
	})

	t.Run("two-dimensional record[] is nested", func(t *testing.T) {
		got, ok := recordColumnValue(m, binaryRecordArray([]int{2, 1}, one, two), oidRecordArray, pgBinaryFormat)
		require.True(t, ok)
		assert.Equal(t, []any{[]any{"(1)"}, []any{"(2)"}}, got)
	})

	t.Run("empty record[]", func(t *testing.T) {
		got, ok := recordColumnValue(m, binaryRecordArray(nil), oidRecordArray, pgBinaryFormat)
		require.True(t, ok)
		assert.Equal(t, []any{}, got)
	})

	t.Run("record[] whose length disagrees with its dimension is left alone", func(t *testing.T) {
		_, ok := recordColumnValue(m, binaryRecordArray([]int{3}, one, two), oidRecordArray, pgBinaryFormat)
		assert.False(t, ok)
	})

	t.Run("other columns are not touched", func(t *testing.T) {
		_, ok := recordColumnValue(m, []byte(rawInt4Dim2), oidInt4Array, pgBinaryFormat)
		assert.False(t, ok)
	})

	t.Run("text format is already PostgreSQL text", func(t *testing.T) {
		_, ok := recordColumnValue(m, []byte("(1,2)"), oidRecord, 0)
		assert.False(t, ok)
	})

	t.Run("NULL stays NULL", func(t *testing.T) {
		_, ok := recordColumnValue(m, nil, oidRecord, pgBinaryFormat)
		assert.False(t, ok)
	})
}

func TestWriteRecordFieldMatchesRecordOut(t *testing.T) {
	for in, want := range map[string]string{
		"abc":     `abc`,
		"":        `""`,
		"a\tb":    "\"a\tb\"",
		"a\vb":    "\"a\vb\"",
		`a"b`:     `"a""b"`,
		`a\b`:     `"a\\b"`,
		"{1,2}":   `"{1,2}"`,
		"é":       `é`,
		"NULL":    `NULL`,
		"(x)":     `"(x)"`,
		"x;y=z'w": `x;y=z'w`,
	} {
		var b strings.Builder
		writeRecordField(&b, in)
		assert.Equal(t, want, b.String(), "input %q", in)
	}
}

// Neon returns PostgreSQL's record text, since pg-types registers no parser for record.
func TestConformance_RecordColumnsAsText(t *testing.T) {
	pg := pgfake.New(t)
	rec := binaryRecord(
		recordField{oid: oidInt4, data: []byte{0, 0, 0, 1}},
		recordField{oid: 25, data: []byte("a b")},
		recordField{oid: oidInt4},
	)
	pg.SetResult("SELECT r, rs FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "r", TypeOID: oidRecord}, {Name: "rs", TypeOID: oidRecordArray}},
		Rows: [][]any{{pgfake.RawColumn(rec), pgfake.RawColumn(binaryRecordArray([]int{2}, rec, nil))}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT r, rs FROM t")
	assert.JSONEq(t, `"(1,\"a b\",)"`, string(rows[0]["r"]))
	assert.JSONEq(t, `["(1,\"a b\",)",null]`, string(rows[0]["rs"]))
}

func TestConformance_RecordFieldsSpelledAsPostgres(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT r FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "r", TypeOID: oidRecord}},
		Rows: [][]any{{pgfake.RawColumn([]byte(recordFieldsFromPostgres[0].raw))}, {pgfake.RawColumn([]byte(recordFieldsFromPostgres[1].raw))}},
		Tag:  "SELECT 2",
	})

	rows := rowsJSON(t, pg, "SELECT r FROM t")
	assert.JSONEq(t, `"(\"2024-01-02 03:04:05+00\",\"10000-01-01 00:00:00\",\"0044-03-15 00:00:00+00 BC\")"`, string(rows[0]["r"]))
	assert.JSONEq(t, `"({01:00:00},\"{\"\"1 day\"\"}\",\"{1e+20,0.5}\",{0.00})"`, string(rows[1]["r"]))
}
