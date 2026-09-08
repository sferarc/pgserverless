package pgserverless

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// The MAC address and bit string types reach the client as PostgreSQL's own
// text. pgx decodes macaddr and macaddr8 to net.HardwareAddr, which is a []byte
// with no MarshalJSON, so encoding/json base64-encoded it: the MAC
// 08:00:2b:01:02:03 arrived as "CAArAQID". That is the bytea failure mode with
// none of bytea's excuse, because a MAC address is a short printable identifier
// a client reads and compares rather than an opaque blob, and "CAArAQID" is a
// string that looks like a value the column could hold.
//
// bit and varbit decode to pgtype.Bits, which marshaled as
// {"Bytes":"oA==","Len":3,"Valid":true}: the same base64, wrapped in a struct
// leaking pgtype's field names, with the bit string recoverable only by knowing
// how pgtype packs it.
func TestConformance_MacaddrAndBitStringsAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			"macaddr", oidMacaddr,
			net.HardwareAddr{0x08, 0x00, 0x2b, 0x01, 0x02, 0x03},
			`"08:00:2b:01:02:03"`,
		},
		{
			"macaddr8", oidMacaddr8,
			net.HardwareAddr{0x08, 0x00, 0x2b, 0x01, 0x02, 0x03, 0x04, 0x05},
			`"08:00:2b:01:02:03:04:05"`,
		},
		{
			// 0xA0 is 10100000; Len 3 takes the leading 101.
			"bit", oidBit,
			pgtype.Bits{Bytes: []byte{0xA0}, Len: 3, Valid: true},
			`"101"`,
		},
		{
			"varbit", oidVarbit,
			pgtype.Bits{Bytes: []byte{0xB0}, Len: 4, Valid: true},
			`"1011"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT c FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "c", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT c FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["c"]))
		})
	}
}

// The array forms. pgx decodes each of these to a []any of the same Go values
// the scalar columns produce, so an uncoerced macaddr[] came back as a list of
// base64 strings and a bit[] as a list of pgtype structs. macaddr8[] is
// deliberately absent: pgx registers no codec for OID 775, so it arrives as one
// raw array literal carrying PostgreSQL's own text, which is a shape difference
// rather than a mangled value. It is recorded in the compat matrix.
func TestConformance_MacaddrAndBitStringArrays(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			"macaddr array", oidMacaddrArray,
			[]net.HardwareAddr{
				{0x08, 0x00, 0x2b, 0x01, 0x02, 0x03},
				{0x08, 0x00, 0x2b, 0x01, 0x02, 0x04},
			},
			`["08:00:2b:01:02:03","08:00:2b:01:02:04"]`,
		},
		{
			"bit array", oidBitArray,
			[]pgtype.Bits{
				{Bytes: []byte{0xA0}, Len: 3, Valid: true},
				{Bytes: []byte{0xC0}, Len: 3, Valid: true},
			},
			`["101","110"]`,
		},
		{
			"varbit array", oidVarbitArray,
			[]pgtype.Bits{
				{Bytes: []byte{0xB0}, Len: 4, Valid: true},
				{Bytes: []byte{0x80}, Len: 1, Valid: true},
			},
			`["1011","1"]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT a FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "a", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["a"]))
		})
	}
}

// A SQL NULL in one of the newly coerced columns stays null. Coercion must not
// invent the empty string for a value that was not there.
func TestConformance_MacaddrNullStaysNull(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidMacaddr}},
		Rows: [][]any{{nil}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT c FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `null`, string(rows[0]["c"]))
}

// Raw-text mode never reaches coerceNeonValue, so it was already returning
// PostgreSQL's words for these columns. This pins the spelling the JSON path was
// made to agree with, so the two paths cannot drift apart.
func TestConformance_MacaddrRawTextUnchanged(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidMacaddr}},
		Rows: [][]any{{net.HardwareAddr{0x08, 0x00, 0x2b, 0x01, 0x02, 0x03}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT c FROM t"}`, map[string]string{
		"Neon-Raw-Text-Output": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"08:00:2b:01:02:03"`, string(rows[0]["c"]))
}

// The unit-level contract, including the nil type map. Without a map these
// values pass through unchanged rather than the call failing or dropping the
// column, which is what pgTextValue has always promised for the types it covers.
func TestCoerceNeonValue_MacaddrAndBitStrings(t *testing.T) {
	m := pgtype.NewMap()
	mac := net.HardwareAddr{0x08, 0x00, 0x2b, 0x01, 0x02, 0x03}
	bits := pgtype.Bits{Bytes: []byte{0xA0}, Len: 3, Valid: true}

	assert.Equal(t, any("08:00:2b:01:02:03"), coerceNeonValue(m, mac, oidMacaddr))
	assert.Equal(t, any("101"), coerceNeonValue(m, bits, oidBit))
	assert.Equal(t, any("101"), coerceNeonValue(m, bits, oidVarbit))

	assert.Equal(t, any(mac), coerceNeonValue(nil, mac, oidMacaddr))
	assert.Equal(t, any(bits), coerceNeonValue(nil, bits, oidBit))

	// A value that already arrived as PostgreSQL text (a type pgx has no codec
	// for, or the simple protocol) is left exactly as it came.
	assert.Equal(t, any("08:00:2b:01:02:03"), coerceNeonValue(m, "08:00:2b:01:02:03", oidMacaddr))
}
