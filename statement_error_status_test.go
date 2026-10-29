package pgserverless

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Neon's proxy answers every statement error with 400 (SqlOverHttpError::Postgres), and the driver reads code only from a 400.
func TestStatementErrorIs400WithItsSQLSTATEWhateverTheClass(t *testing.T) {
	codes := map[string]string{
		"57014": "canceling statement due to statement timeout",
		"53200": "out of memory",
		"53400": "temporary file size exceeds temp_file_limit (1024kB)",
		"57P01": "terminating connection due to administrator command",
		"28000": "permission denied to set session authorization",
		"08006": "connection failure",
	}
	shapes := map[string]struct {
		body    string
		headers map[string]string
	}{
		"single native":   {`{"query":"SELECT fail()"}`, nil},
		"single raw text": {`{"query":"SELECT fail()"}`, map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"}},
		"batch native":    {`{"queries":[{"query":"SELECT 1"},{"query":"SELECT fail()"}]}`, nil},
		"batch raw text":  {`{"queries":[{"query":"SELECT 1"},{"query":"SELECT fail()"}]}`, map[string]string{"Neon-Raw-Text-Output": "true"}},
		"batch with options": {
			`{"queries":[{"query":"SELECT 1"},{"query":"SELECT fail()"}]}`,
			map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Batch-Read-Only": "true"},
		},
	}
	for code, message := range codes {
		pg := pgfake.New(t)
		pg.SetResult("SELECT 1", oneInt(1))
		pg.SetResult("SELECT fail()", pgfake.Result{PgError: &pgproto3.ErrorResponse{
			Severity: "ERROR", Code: code, Message: message,
		}})
		for shapeName, shape := range shapes {
			for name, h := range cancelServers(pg) {
				t.Run(code+"/"+shapeName+"/"+name, func(t *testing.T) {
					rec := doSQL(h, shape.body, shape.headers)

					require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
					resp := decodeError(t, rec)
					assert.Equal(t, message, resp.Message)
					require.NotNil(t, resp.Code)
					assert.Equal(t, code, *resp.Code)
				})
			}
		}
	}
}
