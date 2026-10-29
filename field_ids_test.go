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

// PostgreSQL sends a column's attribute number as a signed int16 and a system
// column's is negative. pgx holds it as a uint16, so ctid's -1 arrived as
// 65535, where Neon (and node-postgres) report -1.
func TestConformance_FieldColumnIDIsSigned(t *testing.T) {
	const usersOID = 16384
	pg := pgfake.New(t)
	pg.SetResult("SELECT ctid, id, 1 AS one FROM users", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "ctid", TypeOID: pgtype.TIDOID, TableOID: usersOID, AttNum: -1},
			{Name: "id", TypeOID: pgtype.Int4OID, TableOID: usersOID, AttNum: 1},
			{Name: "one", TypeOID: pgtype.Int4OID},
		},
		Rows: [][]any{{pgtype.TID{BlockNumber: 0, OffsetNumber: 1, Valid: true}, int32(7), int32(1)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"raw text", map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"}},
		{"native", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doSQL(e, `{"query":"SELECT ctid, id, 1 AS one FROM users"}`, tc.headers)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var resp struct {
				Fields []struct {
					TableID  json.Number `json:"tableID"`
					ColumnID json.Number `json:"columnID"`
				} `json:"fields"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Fields, 3)
			assert.Equal(t, json.Number("-1"), resp.Fields[0].ColumnID)
			assert.Equal(t, json.Number("16384"), resp.Fields[0].TableID)
			assert.Equal(t, json.Number("1"), resp.Fields[1].ColumnID)
			assert.Equal(t, json.Number("0"), resp.Fields[2].ColumnID)
			assert.Equal(t, json.Number("0"), resp.Fields[2].TableID)
		})
	}
}
