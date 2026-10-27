package pgserverless

import (
	"encoding/binary"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"

	"github.com/sferarc/pgserverless/pgfake"
)

// pgNumeric lays a numeric out as numeric_send does: ndigits, weight, sign, dscale, then base-10000 digits.
func pgNumeric(weight int16, sign uint16, dscale int16, digits ...uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, uint16(len(digits)))
	b = binary.BigEndian.AppendUint16(b, uint16(weight))
	b = binary.BigEndian.AppendUint16(b, sign)
	b = binary.BigEndian.AppendUint16(b, uint16(dscale))
	for _, d := range digits {
		b = binary.BigEndian.AppendUint16(b, d)
	}
	return b
}

// binaryNumericArray lays elements out as a numeric[] with the given dimensions; a nil element is NULL.
func binaryNumericArray(dims []int, elems ...[]byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(dims)))
	b = binary.BigEndian.AppendUint32(b, 0)
	b = binary.BigEndian.AppendUint32(b, oidNumeric)
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

var (
	pgZeroScale2 = pgNumeric(0, 0, 2)
	pgOnePoint50 = pgNumeric(0, 0, 2, 1, 5000)
)

func TestNumericZeroText(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
		ok   bool
	}{
		{"0.00", pgZeroScale2, "0.00", true},
		{"0.0", pgNumeric(0, 0, 1), "0.0", true},
		{"scale 0 is left to pgx", pgNumeric(0, 0, 0), "", false},
		{"a value with digits is left to pgx", pgOnePoint50, "", false},
		{"NaN is left to pgx", pgNumeric(0, 0xC000, 0), "", false},
		{"negative dscale is not PostgreSQL's", pgNumeric(0, 0, -1), "", false},
		{"dscale past NUMERIC_DSCALE_MAX is not PostgreSQL's", pgNumeric(0, 0, pgNumericDscaleMax+1), "", false},
		{"truncated header", pgZeroScale2[:6], "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := numericZeroText(tt.raw)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNumericScaleValue(t *testing.T) {
	t.Run("pgx alone drops the scale", func(t *testing.T) {
		m := pgtype.NewMap()
		typ, _ := m.TypeForOID(oidNumeric)
		v, err := typ.Codec.DecodeValue(m, oidNumeric, pgBinaryFormat, pgZeroScale2)
		assert.NoError(t, err)
		assert.Equal(t, "0", coerceNeonValue(m, v, oidNumeric))
	})

	t.Run("scalar zero keeps its scale", func(t *testing.T) {
		assert.Equal(t, "0.00", numericScaleValue("0", pgZeroScale2, oidNumeric, pgBinaryFormat))
	})

	t.Run("text format is already PostgreSQL's spelling", func(t *testing.T) {
		assert.Equal(t, "0", numericScaleValue("0", pgZeroScale2, oidNumeric, 0))
	})

	t.Run("array zeros keep their scale and everything else is untouched", func(t *testing.T) {
		raw := binaryNumericArray([]int{3}, pgZeroScale2, nil, pgOnePoint50)
		got := numericScaleValue([]any{"0", nil, "1.50"}, raw, oidNumericArr, pgBinaryFormat)
		assert.Equal(t, []any{"0.00", nil, "1.50"}, got)
	})

	t.Run("an element count that disagrees leaves the array alone", func(t *testing.T) {
		raw := binaryNumericArray([]int{1}, pgZeroScale2)
		got := numericScaleValue([]any{"0", "0"}, raw, oidNumericArr, pgBinaryFormat)
		assert.Equal(t, []any{"0", "0"}, got)
	})

	t.Run("an array of another element type is left alone", func(t *testing.T) {
		raw := binaryNumericArray([]int{1}, pgZeroScale2)
		binary.BigEndian.PutUint32(raw[8:], oidInt4)
		got := numericScaleValue([]any{"0"}, raw, oidNumericArr, pgBinaryFormat)
		assert.Equal(t, []any{"0"}, got)
	})
}

func TestRecordText_NumericZeroKeepsItsScale(t *testing.T) {
	m := pgtype.NewMap()
	got, ok := recordText(m, binaryRecord(recordField{oid: oidNumeric, data: pgZeroScale2}, recordField{oid: oidNumeric, data: pgOnePoint50}))
	assert.True(t, ok)
	assert.Equal(t, "(0.00,1.50)", got)
}

// PostgreSQL sends a zero numeric with no digits and only its display scale, so a numeric(10,2) balance of 0.00 came back "0" beside "1.50".
func TestConformance_NumericZeroKeepsItsScale(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT n, a, g FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidNumeric}, {Name: "a", TypeOID: oidNumericArr}, {Name: "g", TypeOID: oidNumericArr}},
		Rows: [][]any{{
			pgfake.RawColumn(pgZeroScale2),
			pgfake.RawColumn(binaryNumericArray([]int{3}, pgZeroScale2, nil, pgOnePoint50)),
			pgfake.RawColumn(binaryNumericArray([]int{2, 2}, pgZeroScale2, pgOnePoint50, nil, pgNumeric(0, 0, 3))),
		}},
		Tag: "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT n, a, g FROM t")
	assert.JSONEq(t, `"0.00"`, string(rows[0]["n"]))
	assert.JSONEq(t, `["0.00",null,"1.50"]`, string(rows[0]["a"]))
	assert.JSONEq(t, `[["0.00","1.50"],[null,"0.000"]]`, string(rows[0]["g"]))
}
