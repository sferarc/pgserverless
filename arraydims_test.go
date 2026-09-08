package pgserverless

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// The binary array headers below were captured from a live PostgreSQL on
// 2026-08-29 by running the query in each name against it with pgx and printing
// rows.RawValues(). They are the server's own bytes rather than a hand-built
// approximation, so a change in how PostgreSQL lays out the header would show up
// here as a test that no longer describes reality.
const (
	// select array[array[1,2],array[3,4]]::int4[]
	rawInt4Dim2 = "\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x17" +
		"\x00\x00\x00\x02\x00\x00\x00\x01\x00\x00\x00\x02\x00\x00\x00\x01" +
		"\x00\x00\x00\x04\x00\x00\x00\x01\x00\x00\x00\x04\x00\x00\x00\x02" +
		"\x00\x00\x00\x04\x00\x00\x00\x03\x00\x00\x00\x04\x00\x00\x00\x04"
	// select array[array[array[1],array[2]],array[array[3],array[4]]]::int4[]
	rawInt4Dim3 = "\x00\x00\x00\x03\x00\x00\x00\x00\x00\x00\x00\x17" +
		"\x00\x00\x00\x02\x00\x00\x00\x01\x00\x00\x00\x02\x00\x00\x00\x01" +
		"\x00\x00\x00\x01\x00\x00\x00\x01" +
		"\x00\x00\x00\x04\x00\x00\x00\x01\x00\x00\x00\x04\x00\x00\x00\x02" +
		"\x00\x00\x00\x04\x00\x00\x00\x03\x00\x00\x00\x04\x00\x00\x00\x04"
	// select array[1,2,3]::int4[]
	rawInt4Dim1 = "\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x17" +
		"\x00\x00\x00\x03\x00\x00\x00\x01" +
		"\x00\x00\x00\x04\x00\x00\x00\x01\x00\x00\x00\x04\x00\x00\x00\x02" +
		"\x00\x00\x00\x04\x00\x00\x00\x03"
	// select '{}'::int4[]
	rawInt4Empty = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x17"
	// select row('\x000000020000000000000001'::bytea, 7)
	//
	// Not an array at all. A record is int32 field count, then a uint32 type
	// OID, an int32 length and the data per field, which an array header parser
	// reads as ndim 2 and dimensions [2,1] taken out of the bytea's own bytes.
	// Held here because it is what reshapeArrayValue must refuse.
	rawRecordByteaInt4 = "\x00\x00\x00\x02" +
		"\x00\x00\x00\x11\x00\x00\x00\x0c" +
		"\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x01" +
		"\x00\x00\x00\x17\x00\x00\x00\x04\x00\x00\x00\x07"
)

// recordByteaInt4 is the decoded form of rawRecordByteaInt4, the []any
// pgtype.RecordCodec hands back for it.
var recordByteaInt4 = []any{
	[]byte{0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1},
	int32(7),
}

func TestBinaryArrayDims(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     []int
		wantElem uint32
	}{
		{"two dimensions", rawInt4Dim2, []int{2, 2}, oidInt4},
		{"three dimensions", rawInt4Dim3, []int{2, 2, 1}, oidInt4},
		{"one dimension is not reshaped", rawInt4Dim1, nil, 0},
		{"empty array has no dimensions", rawInt4Empty, nil, 0},
		{"header shorter than the fixed part", "\x00\x00\x00\x02\x00\x00", nil, 0},
		{"header shorter than its dimension list", rawInt4Dim2[:16], nil, 0},
		{
			name: "more dimensions than PostgreSQL allows",
			raw:  "\x00\x00\x00\x07\x00\x00\x00\x00\x00\x00\x00\x17",
			want: nil,
		},
		{
			name: "negative dimension length",
			raw: "\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x17" +
				"\xff\xff\xff\xff\x00\x00\x00\x01\x00\x00\x00\x02\x00\x00\x00\x01",
			want: nil,
		},
		{
			// A record is not an array and this parser cannot see the difference:
			// the field count reads as ndim and the first field's own bytes read
			// as the dimension list. It is why reshapeArrayValue decides from the
			// column's codec and never from these bytes alone.
			name:     "a record's bytes parse as a header, wrongly",
			raw:      rawRecordByteaInt4,
			want:     []int{2, 1},
			wantElem: 12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dims, elem := binaryArrayDims([]byte(tt.raw))
			assert.Equal(t, tt.want, dims)
			assert.Equal(t, tt.wantElem, elem)
		})
	}
}

func TestNestFlatArray(t *testing.T) {
	tests := []struct {
		name string
		flat []any
		dims []int
		want []any
		ok   bool
	}{
		{
			name: "two by two",
			flat: []any{1, 2, 3, 4},
			dims: []int{2, 2},
			want: []any{[]any{1, 2}, []any{3, 4}},
			ok:   true,
		},
		{
			name: "two by three keeps row-major order",
			flat: []any{1, 2, 3, 4, 5, 6},
			dims: []int{2, 3},
			want: []any{[]any{1, 2, 3}, []any{4, 5, 6}},
			ok:   true,
		},
		{
			name: "three dimensions",
			flat: []any{1, 2, 3, 4},
			dims: []int{2, 2, 1},
			want: []any{
				[]any{[]any{1}, []any{2}},
				[]any{[]any{3}, []any{4}},
			},
			ok: true,
		},
		{
			name: "dimensions accounting for too few elements",
			flat: []any{1, 2, 3, 4},
			dims: []int{2, 1},
			ok:   false,
		},
		{
			name: "dimensions accounting for too many elements",
			flat: []any{1, 2, 3},
			dims: []int{2, 2},
			ok:   false,
		},
		{
			name: "a zero-length dimension accounts for nothing",
			flat: []any{1, 2},
			dims: []int{2, 0},
			ok:   false,
		},
		{
			// ((1<<62)+1)*4 overflows a signed 64-bit int and wraps back to
			// exactly 4, the length of flat. A guard that multiplied the
			// dimensions out and compared the product would accept this header
			// and then index far past the slice.
			name: "dimensions whose product overflows",
			flat: []any{1, 2, 3, 4},
			dims: []int{(1 << 62) + 1, 4},
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := nestFlatArray(tt.flat, tt.dims)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestReshapeArrayValue(t *testing.T) {
	m := pgtype.NewMap()
	flat := []any{1, 2, 3, 4}

	t.Run("binary multi-dimensional array is nested", func(t *testing.T) {
		got := reshapeArrayValue(m, flat, []byte(rawInt4Dim2), oidInt4Array, pgBinaryFormat)
		assert.Equal(t, []any{[]any{1, 2}, []any{3, 4}}, got)
	})

	t.Run("text format is left alone", func(t *testing.T) {
		// money[] arrives as a text-format literal and is split by
		// moneyArrayStrings; its braces still carry the dimensions on the wire,
		// so there is nothing here to restore and no header to read.
		got := reshapeArrayValue(m, flat, []byte("{{1,2},{3,4}}"), oidMoneyArray, 0)
		assert.Equal(t, flat, got)
	})

	t.Run("a scalar is left alone", func(t *testing.T) {
		got := reshapeArrayValue(m, "hello", []byte(rawInt4Dim2), oidInt4Array, pgBinaryFormat)
		assert.Equal(t, "hello", got)
	})

	t.Run("one-dimensional array is left alone", func(t *testing.T) {
		one := []any{1, 2, 3}
		assert.Equal(t, one, reshapeArrayValue(m, one, []byte(rawInt4Dim1), oidInt4Array, pgBinaryFormat))
	})

	t.Run("header disagreeing with the value is left alone", func(t *testing.T) {
		short := []any{1, 2, 3}
		assert.Equal(t, short, reshapeArrayValue(m, short, []byte(rawInt4Dim2), oidInt4Array, pgBinaryFormat))
	})

	t.Run("a NULL column stays NULL", func(t *testing.T) {
		assert.Nil(t, reshapeArrayValue(m, nil, nil, oidInt4Array, pgBinaryFormat))
	})

	// A record decodes to []any like an array does, and its bytes parse as a
	// header that accounts for its field count, so nothing but the column's type
	// separates the two. Without the codec gate this returned
	// [[bytea],[7]]: a row shape that is wrong and says nothing about it, which
	// is the failure this function exists to close.
	t.Run("a record column is left alone", func(t *testing.T) {
		got := reshapeArrayValue(m, recordByteaInt4, []byte(rawRecordByteaInt4), oidRecord, pgBinaryFormat)
		assert.Equal(t, recordByteaInt4, got)
	})

	t.Run("an unregistered type is left alone", func(t *testing.T) {
		assert.Equal(t, flat, reshapeArrayValue(m, flat, []byte(rawInt4Dim2), 999999, pgBinaryFormat))
	})

	// The array's own header names its element type, so bytes that belong to
	// another column (or to no array at all) are refused even if they parse.
	t.Run("element OID disagreeing with the column is left alone", func(t *testing.T) {
		assert.Equal(t, flat, reshapeArrayValue(m, flat, []byte(rawInt4Dim2), oidTextArray, pgBinaryFormat))
	})

	t.Run("a nil type map is left alone", func(t *testing.T) {
		assert.Equal(t, flat, reshapeArrayValue(nil, flat, []byte(rawInt4Dim2), oidInt4Array, pgBinaryFormat))
	})
}

// ---------------------------------------------------------------------------
// End to end through the /sql handler
// ---------------------------------------------------------------------------

// arrayFixture builds a multi-dimensional pgtype.Array the fake backend encodes
// on the wire exactly as PostgreSQL would, so pgx decodes it through its real
// codec rather than through a shortcut this test controls.
func arrayFixture[T any](elems []T, dims ...int32) pgtype.Array[T] {
	d := make([]pgtype.ArrayDimension, len(dims))
	for i, n := range dims {
		d[i] = pgtype.ArrayDimension{Length: n, LowerBound: 1}
	}
	return pgtype.Array[T]{Elements: elems, Dims: d, Valid: true}
}

// rowsJSON runs one query against the conformance harness and returns the rows
// of the response, still as raw JSON so the exact serialized shape can be
// asserted.
func rowsJSON(t *testing.T, pg *pgfake.PG, query string) []map[string]json.RawMessage {
	t.Helper()
	e := newConformanceServer(t, pg)
	rec := doSQL(e, `{"query":"`+query+`"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	return rows
}

// A two-dimensional array reaches the client nested, the way the Neon driver
// returns it. pgx decodes every array into a flat []any whatever its
// dimensionality, so without the reshape this row arrived as [1,2,3,4]: a client
// reading a[1] got 2 where PostgreSQL holds [3,4], and nothing in the response
// said a dimension had been dropped.
func TestConformance_MultiDimensionalArray(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4Array}},
		Rows: [][]any{{arrayFixture([]int32{1, 2, 3, 4}, 2, 2)}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT a FROM t")
	assert.JSONEq(t, `[[1,2],[3,4]]`, string(rows[0]["a"]))
}

// Three dimensions nest three deep. Two would be satisfied by any pairing rule;
// this pins the reshape to the dimensions the server actually sent.
func TestConformance_MultiDimensionalArray_ThreeDimensions(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4Array}},
		Rows: [][]any{{arrayFixture([]int32{1, 2, 3, 4, 5, 6}, 3, 2, 1)}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT a FROM t")
	assert.JSONEq(t, `[[[1],[2]],[[3],[4]],[[5],[6]]]`, string(rows[0]["a"]))
}

// The int8 string contract and the array's shape hold at the same time. The
// reshape runs after coerceNeonValue for this reason: nesting first would hide
// every element from the coercion, and the elements would come back as JSON
// numbers, which is the precision loss the string contract exists to prevent.
func TestConformance_MultiDimensionalArray_Int8StaysString(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt8Array}},
		Rows: [][]any{{arrayFixture([]int64{1, 9223372036854775807, 3, 4}, 2, 2)}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT a FROM t")
	assert.JSONEq(t, `[["1","9223372036854775807"],["3","4"]]`, string(rows[0]["a"]))
}

// A NULL element keeps its position rather than shifting every element after it
// into the wrong cell.
func TestConformance_MultiDimensionalArray_NullElement(t *testing.T) {
	one, three, four := int32(1), int32(3), int32(4)
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4Array}},
		Rows: [][]any{{arrayFixture([]*int32{&one, nil, &three, &four}, 2, 2)}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT a FROM t")
	assert.JSONEq(t, `[[1,null],[3,4]]`, string(rows[0]["a"]))
}

// A record column keeps its own shape. It decodes to a []any exactly as an
// array does, and its bytes parse as a two-dimensional array header whose
// dimensions come out of the first field's data, so before the codec gate this
// row came back as [["AAAAAgAAAAAAAAAB"],[7]]. The dimensions were read from
// user data, so nothing had to be crafted for it to fire.
func TestConformance_RecordColumnNotReshaped(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT r FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "r", TypeOID: oidRecord}},
		Rows: [][]any{{pgfake.RawColumn(rawRecordByteaInt4)}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT r FROM t")
	blob := base64.StdEncoding.EncodeToString([]byte{0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.JSONEq(t, `["`+blob+`",7]`, string(rows[0]["r"]))
}

// A one-dimensional array is unchanged. The reshape reads the dimension count
// off the value's own header, so the ordinary case must not gain a nesting level
// it never had.
func TestConformance_OneDimensionalArrayUnchanged(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4Array}},
		Rows: [][]any{{[]int32{1, 2, 3}}},
		Tag:  "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT a FROM t")
	assert.JSONEq(t, `[1,2,3]`, string(rows[0]["a"]))
}
