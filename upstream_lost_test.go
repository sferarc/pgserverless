package pgserverless

import (
	"net/http"
	"testing"

	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC 7.2: a connection to the upstream lost while running is a 400, as Neon's proxy answers any error of its PostgreSQL client.
func TestUpstreamLostMidStatementIs400(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	crash := oneInt(1)
	crash.Disconnect = true
	pg.SetResult("SELECT crash()", crash)

	shapes := map[string]struct {
		body    string
		headers map[string]string
	}{
		"single native":   {`{"query":"SELECT crash()"}`, nil},
		"single raw text": {`{"query":"SELECT crash()"}`, map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"}},
		"batch native":    {`{"queries":[{"query":"SELECT 1"},{"query":"SELECT crash()"}]}`, nil},
		"batch raw text":  {`{"queries":[{"query":"SELECT 1"},{"query":"SELECT crash()"}]}`, map[string]string{"Neon-Raw-Text-Output": "true"}},
		"batch with options": {
			`{"queries":[{"query":"SELECT 1"},{"query":"SELECT crash()"}]}`,
			map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Batch-Read-Only": "true"},
		},
	}
	for shapeName, shape := range shapes {
		for name, h := range cancelServers(pg) {
			t.Run(shapeName+"/"+name, func(t *testing.T) {
				rec := doSQL(h, shape.body, shape.headers)

				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				assert.Equal(t, "upstream connection lost", decodeError(t, rec).Message)
			})
		}
	}
}
