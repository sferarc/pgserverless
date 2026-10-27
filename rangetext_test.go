package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Range and multirange type OIDs, used only to build fixtures. The handler
// itself carries no such table: it reads the element type off the connection's
// own type map, which is what makes it work for every range type pgx registers
// rather than the six spelled out here.
const (
	oidInt4Range      uint32 = 3904
	oidInt4RangeArray uint32 = 3905
	oidNumRange       uint32 = 3906
	oidTsRange        uint32 = 3908
	oidTstzRange      uint32 = 3910
	oidDateRange      uint32 = 3912
	oidInt8Range      uint32 = 3926
	oidInt4Multirange uint32 = 4451
	oidDateMultirange uint32 = 4535
)

func int4Range(lo, hi int32, lowerType, upperType pgtype.BoundType) pgtype.Range[pgtype.Int4] {
	return pgtype.Range[pgtype.Int4]{
		Lower:     pgtype.Int4{Int32: lo, Valid: true},
		Upper:     pgtype.Int4{Int32: hi, Valid: true},
		LowerType: lowerType,
		UpperType: upperType,
		Valid:     true,
	}
}

func numericFor(t *testing.T, s string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	require.NoError(t, n.Scan(s))
	return n
}

// A range column reaches the client as PostgreSQL's own range literal.
//
// pgx decodes every range to pgtype.Range[any], which JSON-marshals as
// {"Lower":1,"Upper":3,"LowerType":105,"UpperType":101,"Valid":true}: the Go
// struct's field names and the bound-type bytes as numbers. That is not a value
// any client can read back, and @neondatabase/serverless returns the literal
// text ("[1,3)") because pg-types has no range parser and hands the raw text
// through.
func TestConformance_RangeAsText(t *testing.T) {
	tests := []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			name:    "int4range",
			typeOID: oidInt4Range,
			value:   int4Range(1, 3, pgtype.Inclusive, pgtype.Exclusive),
			want:    `"[1,3)"`,
		},
		{
			name:    "int4range with an inclusive upper bound",
			typeOID: oidInt4Range,
			value:   int4Range(1, 3, pgtype.Inclusive, pgtype.Inclusive),
			want:    `"[1,3]"`,
		},
		{
			name:    "int4range with an exclusive lower bound",
			typeOID: oidInt4Range,
			value:   int4Range(1, 3, pgtype.Exclusive, pgtype.Exclusive),
			want:    `"(1,3)"`,
		},
		{
			// The bound is past 2^53, where a JSON number would have been
			// rounded. int8 is coerced to a string for exactly this reason and
			// the text form of the range keeps the same guarantee.
			name:    "int8range past the JavaScript integer limit",
			typeOID: oidInt8Range,
			value: pgtype.Range[pgtype.Int8]{
				Lower:     pgtype.Int8{Int64: 1, Valid: true},
				Upper:     pgtype.Int8{Int64: 9007199254740993, Valid: true},
				LowerType: pgtype.Inclusive,
				UpperType: pgtype.Inclusive,
				Valid:     true,
			},
			want: `"[1,9007199254740993]"`,
		},
		{
			name:    "numrange",
			typeOID: oidNumRange,
			value: pgtype.Range[pgtype.Numeric]{
				Lower:     numericFor(t, "1.5"),
				Upper:     numericFor(t, "2.5"),
				LowerType: pgtype.Inclusive,
				UpperType: pgtype.Exclusive,
				Valid:     true,
			},
			want: `"[1.5,2.5)"`,
		},
		{
			name:    "daterange",
			typeOID: oidDateRange,
			value: pgtype.Range[pgtype.Date]{
				Lower:     pgtype.Date{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
				Upper:     pgtype.Date{Time: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), Valid: true},
				LowerType: pgtype.Inclusive,
				UpperType: pgtype.Exclusive,
				Valid:     true,
			},
			want: `"[2024-01-01,2024-02-01)"`,
		},
		{
			// The bounds carry a space, so PostgreSQL quotes them and so does
			// this. An unquoted space would be trimmed by the range parser.
			name:    "tsrange quotes bounds that carry a space",
			typeOID: oidTsRange,
			value: pgtype.Range[pgtype.Timestamp]{
				Lower:     pgtype.Timestamp{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
				Upper:     pgtype.Timestamp{Time: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true},
				LowerType: pgtype.Inclusive,
				UpperType: pgtype.Exclusive,
				Valid:     true,
			},
			want: `"[\"2024-01-01 00:00:00\",\"2024-01-02 00:00:00\")"`,
		},
		{
			// pgx normalizes a timestamptz to UTC and spells the offset "Z";
			// the bound is respelled "+00" as a UTC session prints it.
			name:    "tstzrange",
			typeOID: oidTstzRange,
			value: pgtype.Range[pgtype.Timestamptz]{
				Lower:     pgtype.Timestamptz{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
				Upper:     pgtype.Timestamptz{Time: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true},
				LowerType: pgtype.Inclusive,
				UpperType: pgtype.Exclusive,
				Valid:     true,
			},
			want: `"[\"2024-01-01 00:00:00+00\",\"2024-01-02 00:00:00+00\")"`,
		},
		{
			// A zone other than UTC reaches the bound as the same instant at +00.
			name:    "tstzrange with fractional seconds, a non-UTC zone and an infinite bound",
			typeOID: oidTstzRange,
			value: pgtype.Range[pgtype.Timestamptz]{
				Lower:     pgtype.Timestamptz{Time: time.Date(2024, 1, 1, 2, 0, 0, 500000000, time.FixedZone("", 2*60*60)), Valid: true},
				Upper:     pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true},
				LowerType: pgtype.Inclusive,
				UpperType: pgtype.Exclusive,
				Valid:     true,
			},
			want: `"[\"2024-01-01 00:00:00.5+00\",infinity)"`,
		},
		{
			name:    "an unbounded lower bound is empty, not quoted",
			typeOID: oidInt4Range,
			value: pgtype.Range[pgtype.Int4]{
				Upper:     pgtype.Int4{Int32: 3, Valid: true},
				LowerType: pgtype.Unbounded,
				UpperType: pgtype.Exclusive,
				Valid:     true,
			},
			want: `"(,3)"`,
		},
		{
			name:    "an unbounded range on both sides",
			typeOID: oidInt4Range,
			value: pgtype.Range[pgtype.Int4]{
				LowerType: pgtype.Unbounded,
				UpperType: pgtype.Unbounded,
				Valid:     true,
			},
			want: `"(,)"`,
		},
		{
			name:    "the empty range",
			typeOID: oidInt4Range,
			value: pgtype.Range[pgtype.Int4]{
				LowerType: pgtype.Empty,
				UpperType: pgtype.Empty,
				Valid:     true,
			},
			want: `"empty"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.JSONEq(t, tc.want, queryOneColumn(t, tc.typeOID, tc.value))
		})
	}
}

// A multirange column reaches the client as PostgreSQL's own multirange
// literal. Uncoerced it is worse than the scalar range: pgx decodes it to a
// pgtype.Multirange, so the response carries a JSON *array* of the leaked
// structs and a client indexing it reads objects rather than ranges.
func TestConformance_MultirangeAsText(t *testing.T) {
	tests := []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			name:    "int4multirange",
			typeOID: oidInt4Multirange,
			value: pgtype.Multirange[pgtype.Range[pgtype.Int4]]{
				int4Range(1, 3, pgtype.Inclusive, pgtype.Exclusive),
				int4Range(5, 7, pgtype.Inclusive, pgtype.Exclusive),
			},
			want: `"{[1,3),[5,7)}"`,
		},
		{
			name:    "an empty multirange",
			typeOID: oidInt4Multirange,
			value:   pgtype.Multirange[pgtype.Range[pgtype.Int4]]{},
			want:    `"{}"`,
		},
		{
			// The element bounds are quoted inside the range literals, and the
			// range literals themselves are not quoted inside the braces. That
			// is what PostgreSQL's multirange_out does.
			name:    "datemultirange",
			typeOID: oidDateMultirange,
			value: pgtype.Multirange[pgtype.Range[pgtype.Date]]{
				{
					Lower:     pgtype.Date{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
					Upper:     pgtype.Date{Time: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), Valid: true},
					LowerType: pgtype.Inclusive,
					UpperType: pgtype.Exclusive,
					Valid:     true,
				},
			},
			want: `"{[2024-01-01,2024-02-01)}"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.JSONEq(t, tc.want, queryOneColumn(t, tc.typeOID, tc.value))
		})
	}
}

// An array of ranges reaches the client as an array of range literals, matching
// the scalar contract element by element. pgx decodes it to a []any of
// pgtype.Range values, which would otherwise serialize as an array of structs.
//
// There is no case here for an array of multiranges: pgx registers no codec for
// one (_int4multirange is not in its type map), so that column arrives as the
// raw literal string and is already PostgreSQL's own text.
func TestConformance_RangeArrayAsText(t *testing.T) {
	got := queryOneColumn(t, oidInt4RangeArray, []pgtype.Range[pgtype.Int4]{
		int4Range(1, 3, pgtype.Inclusive, pgtype.Exclusive),
		int4Range(5, 7, pgtype.Inclusive, pgtype.Exclusive),
	})
	assert.JSONEq(t, `["[1,3)","[5,7)"]`, got)
}

// A SQL NULL range stays null rather than becoming the empty range, which is a
// different value: "empty" contains nothing, NULL is the absence of a range.
func TestConformance_RangeNullStaysNull(t *testing.T) {
	assert.JSONEq(t, `null`, queryOneColumn(t, oidInt4Range, nil))
}

// Raw-text mode already returned the literal, because it never decodes. The
// native path now agrees with it, which is the property that matters: the same
// column read two ways is the same value.
func TestConformance_RangeRawTextAgreesWithNativePath(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidInt4Range}},
		Rows: [][]any{{int4Range(1, 3, pgtype.Inclusive, pgtype.Exclusive)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT c FROM t"}`, map[string]string{"Neon-Raw-Text-Output": "true"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `"[1,3)"`, firstColumn(t, rec.Body.Bytes(), "c"))
}

// A bound is quoted whenever the range parser would otherwise read the bare
// text back as something else. The set of characters that forces it is the
// range literal's own punctuation plus whitespace, which is not the array
// literal's set: braces do not matter here and brackets and parentheses do.
func TestQuoteRangeBound(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"a plain bound is left bare", "1", "1"},
		{"the empty string is quoted, since bare it means unbounded", "", `""`},
		{"a comma would end the bound", "a,b", `"a,b"`},
		{"a bracket would end the range", "a]b", `"a]b"`},
		{"a parenthesis would end the range", "a)b", `"a)b"`},
		{"a quote is escaped with a backslash", `a"b`, `"a\"b"`},
		{"a backslash is escaped with a backslash", `a\b`, `"a\\b"`},
		{"a space would be trimmed", "a b", `"a b"`},
		{"a tab would be trimmed", "a\tb", "\"a\tb\""},
		{"a brace is not punctuation in a range", "{a}", "{a}"},
		{
			// PostgreSQL's array parser reads a bare NULL as SQL NULL, so
			// quotePgArrayElem quotes it. A range has no NULL keyword: an
			// absent bound is written as nothing at all, so this stays bare.
			name: "NULL is not a keyword inside a range",
			in:   "NULL",
			want: "NULL",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, quoteRangeBound(tc.in))
		})
	}
}

// rangeLiteral is exercised here with a text element, which no built-in range
// type has, because it is the only way to put a character that needs quoting
// into a bound. The conformance tests above cover the real range types.
func TestRangeLiteral(t *testing.T) {
	m := pgtype.NewMap()
	tests := []struct {
		name  string
		value pgtype.Range[any]
		want  string
		ok    bool
	}{
		{
			name:  "a bound carrying a comma is quoted",
			value: pgtype.Range[any]{Lower: "a,b", Upper: "c", LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true},
			want:  `["a,b",c)`,
			ok:    true,
		},
		{
			name:  "an empty-string bound is quoted, not read as unbounded",
			value: pgtype.Range[any]{Lower: "", Upper: "c", LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true},
			want:  `["",c)`,
			ok:    true,
		},
		{
			name:  "an unbounded end is written as nothing",
			value: pgtype.Range[any]{Upper: "c", LowerType: pgtype.Unbounded, UpperType: pgtype.Exclusive, Valid: true},
			want:  `(,c)`,
			ok:    true,
		},
		{
			name:  "an empty range carries no bounds",
			value: pgtype.Range[any]{LowerType: pgtype.Empty, UpperType: pgtype.Empty, Valid: true},
			want:  "empty",
			ok:    true,
		},
		{
			// rows.Values() returns nil for a SQL NULL rather than an invalid
			// range, so this should not arrive. If it ever does, the value is
			// left as pgx decoded it rather than rendered as something.
			name:  "an invalid range renders nothing",
			value: pgtype.Range[any]{},
			ok:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rangeLiteral(m, tc.value, oidText)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

// Everything that is not a range pgx decoded is left exactly as it was. The
// coercion has to fail closed here: rendering a value it does not understand
// would replace a wrong shape with a wrong value.
func TestRangeTextValue_LeavesEverythingElseAlone(t *testing.T) {
	m := pgtype.NewMap()
	r := pgtype.Range[any]{Lower: 1, Upper: 3, LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true}

	t.Run("a nil type map", func(t *testing.T) {
		_, ok := rangeTextValue(nil, r, oidInt4Range)
		assert.False(t, ok)
	})
	t.Run("a value that is not a range", func(t *testing.T) {
		_, ok := rangeTextValue(m, int32(7), oidInt4)
		assert.False(t, ok)
	})
	t.Run("a range value under an OID the map does not carry", func(t *testing.T) {
		_, ok := rangeTextValue(m, r, 999999)
		assert.False(t, ok)
	})
	t.Run("a range value under a non-range OID", func(t *testing.T) {
		_, ok := rangeTextValue(m, r, oidInt4)
		assert.False(t, ok)
	})
}

// rangeArrayElemOID answers only for arrays whose elements are ranges or
// multiranges. Everything else, including the array types neonArrayElemOID
// already names, is not its business.
func TestRangeArrayElemOID(t *testing.T) {
	m := pgtype.NewMap()
	tests := []struct {
		name     string
		arrayOID uint32
		want     uint32
		ok       bool
	}{
		{"int4range[] is an array of ranges", oidInt4RangeArray, oidInt4Range, true},
		{"int4[] is not", oidInt4Array, 0, false},
		{"a scalar is not an array at all", oidInt4, 0, false},
		{"an OID the map does not carry", 999999, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rangeArrayElemOID(m, tc.arrayOID)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// queryOneColumn runs a one-row, one-column query of the given type through the
// real /sql handler against the fake backend and returns the column's JSON.
func queryOneColumn(t *testing.T, typeOID uint32, value any) string {
	t.Helper()
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: typeOID}},
		Rows: [][]any{{value}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT c FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return firstColumn(t, rec.Body.Bytes(), "c")
}

func firstColumn(t *testing.T, body []byte, name string) string {
	t.Helper()
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	return string(rows[0][name])
}

func TestUTCOffsetText(t *testing.T) {
	tests := map[string]string{
		"2024-01-01 00:00:00Z":    "2024-01-01 00:00:00+00",
		"2024-01-01 00:00:00.5Z":  "2024-01-01 00:00:00.5+00",
		"0044-03-15 12:00:00Z BC": "0044-03-15 12:00:00+00 BC",
		"infinity":                "infinity",
		"2024-01-01 00:00:00+00":  "2024-01-01 00:00:00+00",
	}
	for in, want := range tests {
		assert.Equal(t, want, utcOffsetText(in), in)
	}
}
