package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// "char", tid and xid8 are what catalog introspection and ctid return. pgx
// decodes them to a rune, a pgtype.TID and a uint64, so pg_class.relkind 'r'
// arrived as 114, a ctid as {"BlockNumber":0,"OffsetNumber":1,"Valid":true},
// and an xid8 past 2^53 as a number a JavaScript client rounds.
func TestConformance_CatalogTypesAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{"char", oidQChar, rune('r'), `"r"`},
		{"char zero byte", oidQChar, rune(0), `""`},
		{"char high bit", oidQChar, rune(0xe9), `"\\351"`},
		{"tid", oidTID, pgtype.TID{BlockNumber: 0, OffsetNumber: 1, Valid: true}, `"(0,1)"`},
		{"xid8", oidXID8, uint64(9007199254740993), `"9007199254740993"`},
		{"char array", oidQCharArray, []rune{'r', 'v'}, `["r","v"]`},
		{
			"tid array", oidTIDArray,
			[]pgtype.TID{
				{BlockNumber: 0, OffsetNumber: 1, Valid: true},
				{BlockNumber: 4294967295, OffsetNumber: 65535, Valid: true},
			},
			`["(0,1)","(4294967295,65535)"]`,
		},
		{"xid8 array", oidXID8Array, []uint64{1, 18446744073709551615}, `["1","18446744073709551615"]`},
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

func TestConformance_CatalogTypesNullStaysNull(t *testing.T) {
	for _, typeOID := range []uint32{oidQChar, oidTID, oidXID8} {
		pg := pgfake.New(t)
		pg.SetResult("SELECT c FROM t", pgfake.Result{
			Cols: []pgfake.Col{{Name: "c", TypeOID: typeOID}},
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
		assert.JSONEq(t, `null`, string(rows[0]["c"]), "type %d", typeOID)
	}
}

// Raw-text mode already returned PostgreSQL's text; the JSON path now agrees.
func TestConformance_CatalogTypesRawTextAgrees(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT k, c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "k", TypeOID: oidQChar}, {Name: "c", TypeOID: oidTID}},
		Rows: [][]any{{rune('r'), pgtype.TID{BlockNumber: 0, OffsetNumber: 1, Valid: true}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	for _, headers := range []map[string]string{nil, {"Neon-Raw-Text-Output": "true"}} {
		rec := doSQL(e, `{"query":"SELECT k, c FROM t"}`, headers)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		var rows []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw["rows"], &rows))
		require.Len(t, rows, 1)
		assert.JSONEq(t, `"r"`, string(rows[0]["k"]))
		assert.JSONEq(t, `"(0,1)"`, string(rows[0]["c"]))
	}
}

func TestQCharText(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
		ok   bool
	}{
		{rune('r'), "r", true},
		{rune(0), "", true},
		{rune(0x7f), "\x7f", true},
		{rune(0x80), `\200`, true},
		{rune(0xff), `\377`, true},
		{rune(0x100), "", false},
		{rune(-1), "", false},
		{"r", "", false},
	} {
		got, ok := qcharText(tc.in)
		assert.Equal(t, tc.ok, ok, "%#v", tc.in)
		assert.Equal(t, tc.want, got, "%#v", tc.in)
	}
}

// Without a type map tid and xid8 pass through unchanged; "char" needs no map.
func TestCoerceNeonValue_CatalogTypesNilMap(t *testing.T) {
	tid := pgtype.TID{BlockNumber: 0, OffsetNumber: 1, Valid: true}
	assert.Equal(t, any(tid), coerceNeonValue(nil, tid, oidTID))
	assert.Equal(t, any(uint64(7)), coerceNeonValue(nil, uint64(7), oidXID8))
	assert.Equal(t, any("r"), coerceNeonValue(nil, rune('r'), oidQChar))
}
