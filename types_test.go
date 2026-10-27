package pgserverless

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertTextValue(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		typeOID uint32
		want    any
	}{
		{"nil", nil, oidBool, nil},
		{"bool true", []byte("t"), oidBool, true},
		{"bool false", []byte("f"), oidBool, false},
		{"int2", []byte("42"), oidInt2, 42},
		{"int4", []byte("-1"), oidInt4, -1},
		{"int8 as string", []byte("9223372036854775807"), oidInt8, "9223372036854775807"},
		{"float4", []byte("3.14"), oidFloat4, 3.14},
		{"float8", []byte("2.718281828"), oidFloat8, 2.718281828},
		{"numeric as string", []byte("123456.789"), oidNumeric, "123456.789"},
		{"json", []byte(`{"key":"val"}`), oidJSON, map[string]any{"key": "val"}},
		{"jsonb", []byte(`[1,2,3]`), oidJSONB, []any{float64(1), float64(2), float64(3)}},
		{"text", []byte("hello"), 25, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertTextValue(tt.raw, tt.typeOID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParsePgArray(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		elemOID uint32
		want    []any
		wantErr bool
	}{
		{"empty array", "{}", 25, []any{}, false},
		{"int array", "{1,2,3}", oidInt4, []any{1, 2, 3}, false},
		{"text array", `{"hello","world"}`, 25, []any{"hello", "world"}, false},
		{"null element", "{1,NULL,3}", oidInt4, []any{1, nil, 3}, false},
		{"quoted NULL string", `{"NULL","hello"}`, 25, []any{"NULL", "hello"}, false},
		{"bool array", "{t,f,t}", oidBool, []any{true, false, true}, false},
		{"two dimensions", "{{1,2},{3,4}}", oidInt4, []any{[]any{1, 2}, []any{3, 4}}, false},
		{"three dimensions", "{{{1},{2}},{{3},{4}}}", oidInt4, []any{
			[]any{[]any{1}, []any{2}},
			[]any{[]any{3}, []any{4}},
		}, false},
		{"nested null elements", "{{1,NULL},{NULL,4}}", oidInt4, []any{[]any{1, nil}, []any{nil, 4}}, false},
		{"nested empty text elements", `{{"",a},{b,""}}`, 25, []any{[]any{"", "a"}, []any{"b", ""}}, false},
		{"quoted commas and braces", `{"a,b","{c}","d}"}`, 25, []any{"a,b", "{c}", "d}"}, false},
		{"quoted commas inside a nested array", `{{"$1,000.00",$2.00},{$3.00,"$4,000.00"}}`, oidMoney, []any{
			[]any{"$1,000.00", "$2.00"},
			[]any{"$3.00", "$4,000.00"},
		}, false},
		{"escaped quote and backslash", `{"a\"b","c\\d"}`, 25, []any{`a"b`, `c\d`}, false},
		{"dimension decoration is skipped", "[0:1]={1,2}", oidInt4, []any{1, 2}, false},
		{"invalid", "not an array", 25, nil, true},
		{"unbalanced braces", "{{1,2},{3,4}", oidInt4, nil, true},
		{"trailing text", "{1,2}x", oidInt4, nil, true},
		{"unterminated quote", `{"a}`, 25, nil, true},
		{"bare brace inside an element", "{a{b}", 25, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePgArray([]byte(tt.raw), tt.elemOID)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConvertParams(t *testing.T) {
	tests := []struct {
		name   string
		params []any
		want   []any
	}{
		{"nil", nil, nil},
		{"empty", []any{}, []any{}},
		{"scalars", []any{1, "hello", true, false}, []any{1, "hello", "true", "false"}},
		{"slice to pg array", []any{[]any{1, 2, 3}}, []any{"{1,2,3}"}},
		{"map to json", []any{map[string]any{"k": "v"}}, []any{`{"k":"v"}`}},
		{"floats to text", []any{1.5, float64(1000000), 1e21, float32(0.5), float32(0.1)}, []any{"1.5", "1000000", "1000000000000000000000", "0.5", "0.1"}},
		{"non-finite floats to text", []any{math.NaN(), math.Inf(1), math.Inf(-1)}, []any{"NaN", "Infinity", "-Infinity"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertParams(tt.params)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatPgArray(t *testing.T) {
	tests := []struct {
		name string
		arr  []any
		want string
	}{
		{"empty", []any{}, "{}"},
		{"ints", []any{1, 2, 3}, "{1,2,3}"},
		{"with null", []any{1, nil, 3}, "{1,NULL,3}"},
		{"strings needing quotes", []any{"hello,world", "foo"}, `{"hello,world",foo}`},
		{"NULL string", []any{"NULL"}, `{"NULL"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatPgArray(tt.arr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatPgArray_NonFinite(t *testing.T) {
	got := formatPgArray([]any{math.NaN(), math.Inf(1), math.Inf(-1)})
	assert.Equal(t, `{"NaN","Infinity","-Infinity"}`, got)
}

// coerceNeonValue is the one place the JSON result path realigns pgx's native
// Go decode with what @neondatabase/serverless returns. Every input below is
// the Go value pgx actually produced for that column against a real
// PostgreSQL, and every want is the value the driver returns for it.
func TestCoerceNeonValue(t *testing.T) {
	// The bytes PostgreSQL sent for a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11.
	uuidBytes := [16]byte{
		0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8,
		0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11,
	}
	const uuidText = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"

	tests := []struct {
		name    string
		v       any
		typeOID uint32
		want    any
	}{
		{"null stays null", nil, oidUUID, nil},

		// pgx decodes uuid to [16]byte, which encoding/json renders as 16
		// numbers rather than as the string the driver returns.
		{"uuid is its dashed text", uuidBytes, oidUUID, uuidText},
		{"uuid array is dashed text element-wise", []any{uuidBytes}, oidUUIDArray, []any{uuidText}},
		{"uuid already text is left alone", uuidText, oidUUID, uuidText},

		// pgx registers no codec for money[], so the column arrives as one raw
		// array literal string rather than as a slice.
		{"money array splits", "{$1.23,$2.34}", oidMoneyArray, []any{"$1.23", "$2.34"}},
		{"money array keeps sql null", "{$1.23,NULL}", oidMoneyArray, []any{"$1.23", nil}},
		{"money array can be empty", "{}", oidMoneyArray, []any{}},
		// PostgreSQL quotes an element containing its own group separators, so
		// splitting on commas would tear this one into five.
		{
			"money array element with group separators stays whole",
			`{"$92,233,720,368,547,758.07"}`,
			oidMoneyArray,
			[]any{"$92,233,720,368,547,758.07"},
		},
		{
			"money array keeps its dimensions",
			"{{$1.23,$2.34},{$3.45,$4.56}}",
			oidMoneyArray,
			[]any{[]any{"$1.23", "$2.34"}, []any{"$3.45", "$4.56"}},
		},
		{
			"macaddr8 array splits",
			"{08:00:2b:01:02:03:04:05,NULL}",
			oidMacaddr8Array,
			[]any{"08:00:2b:01:02:03:04:05", nil},
		},

		// pgx decodes bytea and xml to []byte, which encoding/json base64-encodes,
		// so the client is handed "AQI=" for '\x0102' with nothing marking it.
		{"bytea is its hex text", []byte{0x01, 0x02}, oidBytea, `\x0102`},
		{"bytea array is hex text element-wise", []any{[]byte{0x01, 0x02}}, oidByteaArray, []any{`\x0102`}},
		{"xml is its own text", []byte("<a/>"), oidXML, "<a/>"},

		// pgx decodes date to time.Time, which marshals as an RFC 3339 instant
		// for a type that carries no time at all.
		{"date drops the invented time", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), oidDate, "2024-01-01"},

		// pgx decodes time and interval to structs, which marshal as JSON
		// objects leaking the pgtype field names into the response.
		{"time is text, not a struct", pgtype.Time{Microseconds: 45296000000, Valid: true}, oidTime, "12:34:56"},
		{"interval is text, not a struct", pgtype.Interval{Days: 1, Valid: true}, oidInterval, "1 day"},

		// A value pgx has no codec for arrives as PostgreSQL text already.
		{"already-text value is left alone", "12:34:56", oidTime, "12:34:56"},

		// Unchanged behavior, kept here so a regression in either shows up.
		{"int8 is a string", int64(9007199254740993), oidInt8, "9007199254740993"},
		{"int8 array is strings", []any{int64(1)}, oidInt8Array, []any{"1"}},
		{"money scalar is a string", "$1.23", oidMoney, "$1.23"},
		{"text passes through", "hello", 25, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, coerceNeonValue(pgtype.NewMap(), tt.v, tt.typeOID))
		})
	}
}

// Without a type map the text-rendered types pass through unchanged rather than
// the call failing or dropping the column. Nothing in the handler passes nil
// today; this pins the contract pgTextValue documents.
func TestCoerceNeonValue_NilTypeMapPassesThrough(t *testing.T) {
	v := []byte{0x01, 0x02}
	assert.Equal(t, any(v), coerceNeonValue(nil, v, oidBytea))
}

// A uuid must reach the client as a JSON string. Before the coercion it
// marshaled as a 16-number array, which is not a value any client parses back.
func TestCoerceNeonValue_UUIDMarshalsAsString(t *testing.T) {
	uuidBytes := [16]byte{
		0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8,
		0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11,
	}
	b, err := json.Marshal(coerceNeonValue(pgtype.NewMap(), uuidBytes, oidUUID))
	require.NoError(t, err)
	assert.JSONEq(t, `"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"`, string(b))
}

func TestParseCommandTag(t *testing.T) {
	tests := []struct {
		tag      string
		wantCmd  string
		wantRows *int
	}{
		{"SELECT 5", "SELECT", new(5)},
		{"INSERT 0 1", "INSERT", new(1)},
		{"UPDATE 3", "UPDATE", new(3)},
		{"DELETE 0", "DELETE", new(0)},
		{"CREATE TABLE", "CREATE", nil},
		{"", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			cmd, rows := parseCommandTag(tt.tag)
			assert.Equal(t, tt.wantCmd, cmd)
			assert.Equal(t, tt.wantRows, rows)
		})
	}
}

//go:fix inline
func intPtr(n int) *int { return new(n) }
