package pgserverless

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// pgx has no plan to encode a Go bool into a text, varchar or int4 parameter, so a JSON true failed before reaching PostgreSQL.
func TestConformance_BoolParamsBindAsText(t *testing.T) {
	for _, mode := range []struct {
		name    string
		headers map[string]string
	}{
		{"native", nil},
		{"raw text", driverHeaders},
	} {
		for _, param := range []struct {
			cast string
			oid  uint32
		}{
			{"text", 25},
			{"varchar", 1043},
			{"bool", oidBool},
			{"int4", oidInt4},
		} {
			t.Run(mode.name+"/"+param.cast, func(t *testing.T) {
				query := "SELECT $1::" + param.cast + " AS v"
				pg := pgfake.New(t)
				pg.SetResult(query, pgfake.Result{
					Cols:      []pgfake.Col{{Name: "v", TypeOID: 25}},
					Rows:      [][]any{{"true"}},
					Tag:       "SELECT 1",
					ParamOIDs: []uint32{param.oid},
				})
				e := newConformanceServer(t, pg)

				rec := doSQL(e, `{"query":"`+query+`","params":[true]}`, mode.headers)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.Equal(t, [][]byte{[]byte("true")}, pg.LastBindParams())
			})
		}
	}
}
