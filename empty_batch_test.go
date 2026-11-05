package pgserverless

import (
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Client 1.1.0 posts {"queries":[]} for sql.transaction([]); Neon's proxy runs
// BEGIN and COMMIT and answers 200 with no results (SPEC 4.3).
func TestEmptyBatchIsAnsweredWithNoResults(t *testing.T) {
	characteristics := map[string]string{
		"Neon-Batch-Read-Only":       "true",
		"Neon-Batch-Isolation-Level": "Serializable",
	}
	driverWithCharacteristics := maps.Clone(driverHeaders)
	maps.Copy(driverWithCharacteristics, characteristics)

	servers := map[string]func(*pgfake.PG) http.Handler{
		"extended": func(pg *pgfake.PG) http.Handler { return newConformanceServer(t, pg) },
		"governed": func(pg *pgfake.PG) http.Handler { return newGovernedConformanceServer(pg, semicolonCount) },
	}
	headerSets := map[string]map[string]string{
		"native":                      nil,
		"native with characteristics": characteristics,
		"driver headers":              driverHeaders,
		"driver with characteristics": driverWithCharacteristics,
	}
	for server, newServer := range servers {
		for name, headers := range headerSets {
			t.Run(server+"/"+name, func(t *testing.T) {
				pg := pgfake.New(t)
				rec := doSQL(newServer(pg), `{"queries":[]}`, headers)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.JSONEq(t, `{"results":[]}`, rec.Body.String())
				if headers["Neon-Batch-Read-Only"] == "true" {
					assert.Equal(t, "true", rec.Header().Get("Neon-Batch-Read-Only"))
					assert.Equal(t, "Serializable", rec.Header().Get("Neon-Batch-Isolation-Level"))
					assert.Equal(t, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY", pg.LastBegin())
				}
			})
		}
	}
}
