package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// orderedBatchBackend serves a batch whose second and third items use the table its first item creates.
func orderedBatchBackend(t *testing.T) *pgfake.PG {
	t.Helper()
	pg := pgfake.New(t)
	pg.SetResult("CREATE TABLE t (a int)", pgfake.Result{Tag: "CREATE TABLE", IsWrite: true})
	pg.SetResult("INSERT INTO t VALUES ($1)", pgfake.Result{Tag: "INSERT 0 1", IsWrite: true, Requires: "CREATE TABLE t (a int)"})
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols:     []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows:     [][]any{{int32(7)}},
		Tag:      "SELECT 1",
		Requires: "CREATE TABLE t (a int)",
	})
	return pg
}

const orderedBatch = `{"queries":[
	{"query":"CREATE TABLE t (a int)"},
	{"query":"INSERT INTO t VALUES ($1)","params":["7"]},
	{"query":"SELECT a FROM t"}]}`

// Neon runs a batch's statements one after another, so an item can use what an
// earlier one created. pgx's cached-statement batch prepared every item before
// running the first, and the INSERT failed with 42P01.
func TestBatchItemsRunInOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"native", nil},
		{"native with options", map[string]string{"Neon-Batch-Isolation-Level": "Serializable"}},
		{"raw text", map[string]string{"Neon-Raw-Text-Output": "true"}},
		{"raw text with options", map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Batch-Isolation-Level": "Serializable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := orderedBatchBackend(t)
			rec := doSQL(newConformanceServer(t, pg), orderedBatch, tc.headers)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var resp BatchResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Results, 3)
			assert.Equal(t, "CREATE", resp.Results[0].Command)
			assert.Equal(t, "INSERT", resp.Results[1].Command)
			require.Len(t, resp.Results[2].Rows, 1)
			assert.Equal(t, []string{"CREATE TABLE t (a int)", "INSERT INTO t VALUES ($1)"}, pg.CommittedWrites())
		})
	}
}

// Statement by statement, the native batch is one transaction: a failing item
// leaves no earlier write behind, and the characteristics are applied with BEGIN.
func TestNativeBatchIsOneTransaction(t *testing.T) {
	pg := orderedBatchBackend(t)
	pg.SetResult("SELECT boom", pgfake.Result{PgError: &pgproto3.ErrorResponse{Severity: "ERROR", Code: "22012", Message: "division by zero"}})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"CREATE TABLE t (a int)"},{"query":"SELECT boom"}]}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	require.NotNil(t, errResp.Code)
	assert.Equal(t, "22012", *errResp.Code)
	assert.Empty(t, pg.CommittedWrites())

	rec = doSQL(e, orderedBatch, map[string]string{"Neon-Batch-Isolation-Level": "RepeatableRead"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "BEGIN ISOLATION LEVEL REPEATABLE READ", pg.LastBegin())
	assert.Equal(t, "RepeatableRead", rec.Header().Get("Neon-Batch-Isolation-Level"))
}
