package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Neon opens every batch with BEGIN. A raw-text batch without options ran as an
// implicit-transaction pipeline, which PostgreSQL does not count as a
// transaction block, so LOCK TABLE, SAVEPOINT and DECLARE CURSOR failed with 25P01.
func TestBatchRunsInsideATransactionBlock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		begin   string
	}{
		{"native", nil, "BEGIN"},
		{"raw text", map[string]string{"Neon-Raw-Text-Output": "true"}, "BEGIN"},
		{"raw text with options", map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Batch-Isolation-Level": "Serializable"}, "BEGIN ISOLATION LEVEL SERIALIZABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("LOCK TABLE accounts IN EXCLUSIVE MODE", pgfake.Result{Tag: "LOCK TABLE", NeedsTxnBlock: true})
			pg.SetResult("SAVEPOINT s1", pgfake.Result{Tag: "SAVEPOINT", NeedsTxnBlock: true})
			pg.SetResult("UPDATE accounts SET n = n + 1", pgfake.Result{Tag: "UPDATE 1", IsWrite: true})

			rec := doSQL(newConformanceServer(t, pg), `{"queries":[
				{"query":"LOCK TABLE accounts IN EXCLUSIVE MODE"},
				{"query":"SAVEPOINT s1"},
				{"query":"UPDATE accounts SET n = n + 1"}]}`, tc.headers)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var resp BatchResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Results, 3)
			assert.Equal(t, "LOCK", resp.Results[0].Command)
			assert.Equal(t, "UPDATE", resp.Results[2].Command)
			assert.Equal(t, tc.begin, pg.LastBegin())
			assert.Equal(t, []string{"UPDATE accounts SET n = n + 1"}, pg.CommittedWrites())
		})
	}
}

// The fake refuses such a statement outside BEGIN, so the test above would catch a return to the implicit pipeline.
func TestPgfakeRefusesTransactionBlockStatementOutsideBegin(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SAVEPOINT s1", pgfake.Result{Tag: "SAVEPOINT", NeedsTxnBlock: true})

	rec := doSQL(newConformanceServer(t, pg), `{"query":"SAVEPOINT s1"}`, map[string]string{"Neon-Raw-Text-Output": "true"})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "25P01", *resp.Code)
}
