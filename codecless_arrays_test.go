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

// Each literal is what PostgreSQL 17.11 prints for the array, and want is the
// JSON array Neon returns for it: every element as its own text.
func TestConformance_CodeclessArraysAsStrings(t *testing.T) {
	tests := []struct {
		name    string
		typeOID uint32
		literal string
		want    string
	}{
		{"timetz[] with a NULL", 1270, `{12:34:56+02,NULL,00:00:00.5-05:30}`, `["12:34:56+02",null,"00:00:00.5-05:30"]`},
		{"two-dimensional pg_lsn[]", 3221, `{{0/16B3748,1/0},{FF/1,0/0}}`, `[["0/16B3748","1/0"],["FF/1","0/0"]]`},
		{"regclass[] with a quoted identifier", 2210, `{"\"My Table\"",pg_class}`, `["\"My Table\"","pg_class"]`},
		{"tsquery[]", 3645, `{"'fat' & 'rat'","'a b' | 'c'"}`, `["'fat' & 'rat'","'a b' | 'c'"]`},
		{"txid_snapshot[]", 2949, `{"10:20:10,14,15"}`, `["10:20:10,14,15"]`},
		{
			"tstzmultirange[]", 6153,
			`{"{[\"2024-01-01 01:00:00+01\",\"2024-01-02 01:00:00+01\")}","{}"}`,
			`["{[\"2024-01-01 01:00:00+01\",\"2024-01-02 01:00:00+01\")}","{}"]`,
		},
		{"empty regtype[]", 2211, `{}`, `[]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.JSONEq(t, tc.want, codeclessArrayColumn(t, tc.typeOID, tc.literal, nil))
		})
	}
}

func TestConformance_CodeclessArrayRawTextUnchanged(t *testing.T) {
	got := codeclessArrayColumn(t, 1270, `{12:34:56+02,NULL}`, map[string]string{"Neon-Raw-Text-Output": "true"})
	assert.JSONEq(t, `"{12:34:56+02,NULL}"`, got)
}

// A literal the parser refuses is returned as the string it arrived as.
func TestCoerceNeonValue_CodeclessArrayMalformedLiteral(t *testing.T) {
	assert.Equal(t, "{12:34:56+02", coerceNeonValue(pgtype.NewMap(), "{12:34:56+02", 1270))
}

// Once pgx registers one of these, the column decodes to a slice and must leave the table.
func TestCodeclessArrayElemOIDHasNoPgxCodec(t *testing.T) {
	m := pgtype.NewMap()
	for arrOID, elemOID := range codeclessArrayElemOID {
		_, known := m.TypeForOID(arrOID)
		assert.False(t, known, "pgx now has a codec for array OID %d", arrOID)
		assert.NotEqual(t, arrOID, elemOID)
	}
}

func codeclessArrayColumn(t *testing.T, typeOID uint32, literal string, headers map[string]string) string {
	t.Helper()
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: typeOID}},
		Rows: [][]any{{literal}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	return string(rows[0]["a"])
}
