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
		assert.Equal(t, "0.00", numericScaleValue(nil, "0", pgZeroScale2, oidNumeric, pgBinaryFormat))
	})

	t.Run("text format is already PostgreSQL's spelling", func(t *testing.T) {
		assert.Equal(t, "0", numericScaleValue(nil, "0", pgZeroScale2, oidNumeric, 0))
	})

	t.Run("array zeros keep their scale and everything else is untouched", func(t *testing.T) {
		raw := binaryNumericArray([]int{3}, pgZeroScale2, nil, pgOnePoint50)
		got := numericScaleValue(nil, []any{"0", nil, "1.50"}, raw, oidNumericArr, pgBinaryFormat)
		assert.Equal(t, []any{"0.00", nil, "1.50"}, got)
	})

	t.Run("an element count that disagrees leaves the array alone", func(t *testing.T) {
		raw := binaryNumericArray([]int{1}, pgZeroScale2)
		got := numericScaleValue(nil, []any{"0", "0"}, raw, oidNumericArr, pgBinaryFormat)
		assert.Equal(t, []any{"0", "0"}, got)
	})

	t.Run("an array of another element type is left alone", func(t *testing.T) {
		raw := binaryNumericArray([]int{1}, pgZeroScale2)
		binary.BigEndian.PutUint32(raw[8:], oidInt4)
		got := numericScaleValue(nil, []any{"0"}, raw, oidNumericArr, pgBinaryFormat)
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

// binaryNumrange lays a numrange out as range_send does: the flag byte, then each finite bound with its length.
func binaryNumrange(flags byte, bounds ...[]byte) []byte {
	b := []byte{flags}
	for _, bound := range bounds {
		b = binary.BigEndian.AppendUint32(b, uint32(len(bound)))
		b = append(b, bound...)
	}
	return b
}

// binaryNummultirange lays ranges out as multirange_send does: a count, then each range with its length.
func binaryNummultirange(ranges ...[]byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(ranges)))
	for _, r := range ranges {
		b = binary.BigEndian.AppendUint32(b, uint32(len(r)))
		b = append(b, r...)
	}
	return b
}

func binaryNumrangeArray(dims []int, elems ...[]byte) []byte {
	raw := binaryNumericArray(dims, elems...)
	binary.BigEndian.PutUint32(raw[8:], pgtype.NumrangeOID)
	return raw
}

var (
	pgTwoPoint5         = pgNumeric(0, 0, 1, 2, 5000)
	numrangeZeroToOne50 = binaryNumrange(rangeFlagLowerInclusive, pgZeroScale2, pgOnePoint50)
)

func TestNumericScaleText_Ranges(t *testing.T) {
	m := pgtype.NewMap()
	tests := []struct {
		name string
		oid  uint32
		raw  []byte
		want string
		ok   bool
	}{
		{"numrange with a zero lower bound", pgtype.NumrangeOID, numrangeZeroToOne50, "[0.00,1.50)", true},
		{"numrange with a zero upper bound and no lower bound", pgtype.NumrangeOID,
			binaryNumrange(rangeFlagLowerInfinite|rangeFlagUpperInclusive, pgZeroScale2), "(,0.00]", true},
		{"numrange with no zero is left to pgx", pgtype.NumrangeOID, binaryNumrange(rangeFlagLowerInclusive, pgOnePoint50, pgTwoPoint5), "", false},
		{"empty numrange is left to pgx", pgtype.NumrangeOID, []byte{rangeFlagEmpty}, "", false},
		{"truncated numrange", pgtype.NumrangeOID, numrangeZeroToOne50[:len(numrangeZeroToOne50)-1], "", false},
		{"trailing bytes after the bounds", pgtype.NumrangeOID, append(append([]byte{}, numrangeZeroToOne50...), 0), "", false},
		{"nummultirange with a zero bound", pgtype.NummultirangeOID,
			binaryNummultirange(numrangeZeroToOne50, binaryNumrange(rangeFlagLowerInclusive|rangeFlagUpperInfinite, pgTwoPoint5)), "{[0.00,1.50),[2.5,)}", true},
		{"nummultirange with no zero is left to pgx", pgtype.NummultirangeOID,
			binaryNummultirange(binaryNumrange(rangeFlagLowerInclusive, pgOnePoint50, pgTwoPoint5)), "", false},
		{"empty nummultirange is left to pgx", pgtype.NummultirangeOID, binaryNummultirange(), "", false},
		{"nummultirange claiming more ranges than it holds", pgtype.NummultirangeOID,
			binary.BigEndian.AppendUint32(nil, 1<<31), "", false},
		{"an int4range is not touched", oidInt4Range, numrangeZeroToOne50, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := numericScaleText(m, tt.raw, tt.oid)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("no type map leaves a nonzero bound unrendered", func(t *testing.T) {
		_, ok := numericScaleText(nil, numrangeZeroToOne50, pgtype.NumrangeOID)
		assert.False(t, ok)
	})
}

func TestNumericScaleValue_NumrangeArray(t *testing.T) {
	m := pgtype.NewMap()
	raw := binaryNumrangeArray([]int{3}, numrangeZeroToOne50, nil, binaryNumrange(rangeFlagLowerInclusive, pgOnePoint50, pgTwoPoint5))
	got := numericScaleValue(m, []any{"[0,1.50)", nil, "[1.50,2.5)"}, raw, pgtype.NumrangeArrayOID, pgBinaryFormat)
	assert.Equal(t, []any{"[0.00,1.50)", nil, "[1.50,2.5)"}, got)
}

func TestRecordText_NumrangeZeroKeepsItsScale(t *testing.T) {
	m := pgtype.NewMap()
	got, ok := recordText(m, binaryRecord(recordField{oid: pgtype.NumrangeOID, data: numrangeZeroToOne50}))
	assert.True(t, ok)
	assert.Equal(t, `("[0.00,1.50)")`, got)
}

// pgx decodes a binary numeric zero without its display scale, so numrange(0.00, 1.50) came back "[0,1.50)" where PostgreSQL prints "[0.00,1.50)".
func TestConformance_NumrangeZeroKeepsItsScale(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT r, mr, ra FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "r", TypeOID: pgtype.NumrangeOID},
			{Name: "mr", TypeOID: pgtype.NummultirangeOID},
			{Name: "ra", TypeOID: pgtype.NumrangeArrayOID},
		},
		Rows: [][]any{{
			pgfake.RawColumn(numrangeZeroToOne50),
			pgfake.RawColumn(binaryNummultirange(numrangeZeroToOne50, binaryNumrange(rangeFlagLowerInclusive, pgTwoPoint5, pgNumeric(0, 0, 1, 3)))),
			pgfake.RawColumn(binaryNumrangeArray([]int{2, 1}, numrangeZeroToOne50, binaryNumrange(rangeFlagLowerInclusive, pgOnePoint50, pgTwoPoint5))),
		}},
		Tag: "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT r, mr, ra FROM t")
	assert.JSONEq(t, `"[0.00,1.50)"`, string(rows[0]["r"]))
	assert.JSONEq(t, `"{[0.00,1.50),[2.5,3.0)}"`, string(rows[0]["mr"]))
	assert.JSONEq(t, `[["[0.00,1.50)"],["[1.50,2.5)"]]`, string(rows[0]["ra"]))
}
