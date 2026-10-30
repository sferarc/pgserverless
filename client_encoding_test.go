package pgserverless

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func TestDialPinsClientEncodingToUTF8(t *testing.T) {
	clearLibpqEnv(t)
	pg := pgfake.New(t)

	conn, err := Dial(context.Background(), Upstream{Addr: pg.Addr(), User: "u", Database: "db", InsecureSkipVerify: true}, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	assert.Equal(t, "UTF8", pg.StartupParam("client_encoding"))
}

// A session's client_encoding defaults to the database's, so a LATIN1 database sent its text unconverted into the UTF-8 JSON.
func TestSQLReturnsNonASCIITextFromALatin1Database(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"native":   nil,
		"raw text": {"Neon-Raw-Text-Output": "true"},
	} {
		t.Run(name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetDatabaseEncoding("LATIN1")
			pg.SetResult("SELECT name FROM cafes", pgfake.Result{
				Cols: []pgfake.Col{{Name: "name", TypeOID: oidText}},
				Rows: [][]any{{"Café Müller"}},
			})

			rec := doSQL(newConformanceServer(t, pg), `{"query":"SELECT name FROM cafes"}`, headers)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var got struct {
				Rows []map[string]string `json:"rows"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Len(t, got.Rows, 1)
			assert.Equal(t, "Café Müller", got.Rows[0]["name"])
		})
	}
}
