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

// oid, xid and cid reach a native-mode client as decimal strings, as Neon's
// server returns them. pgx decodes all three to a uint32 and encoding/json wrote
// a number, so pg_class.oid and a row's xmin came back in a shape Neon never
// produces: its native mode emits a JSON number only for int2 and int4.
func TestConformance_CatalogIDsAsStrings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{"oid", oidOID, uint32(1259), `"1259"`},
		{"xid", oidXID, uint32(4294967295), `"4294967295"`},
		{"cid", oidCID, uint32(0), `"0"`},
		{"oid array", oidOIDArray, []uint32{16, 23}, `["16","23"]`},
		{"xid array", oidXIDArray, []uint32{731, 732}, `["731","732"]`},
		{"cid array", oidCIDArray, []uint32{1}, `["1"]`},
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

// A NULL element stays null rather than becoming a string.
func TestConformance_CatalogIDArrayNullElement(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidOIDArray}},
		Rows: [][]any{{[]*uint32{new(uint32), nil}}},
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
	assert.JSONEq(t, `["0",null]`, string(rows[0]["c"]))
}

// int4 is the neighbouring type Neon does emit as a number, and must stay one.
func TestCoerceNeonValue_CatalogIDs(t *testing.T) {
	m := pgtype.NewMap()

	assert.Equal(t, any("1259"), coerceNeonValue(m, uint32(1259), oidOID))
	assert.Equal(t, any("7"), coerceNeonValue(nil, uint32(7), oidXID))
	assert.Equal(t, any("3"), coerceNeonValue(nil, uint32(3), oidCID))
	assert.Equal(t, any(int32(1259)), coerceNeonValue(m, int32(1259), oidInt4))

	// A value that already arrived as text, as on the simple protocol, is left alone.
	assert.Equal(t, any("1259"), coerceNeonValue(m, "1259", oidOID))
}
