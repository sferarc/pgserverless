package pgserverless

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// json.Marshal wrote <, > and & as \u003c, \u003e and \u0026, which a json or text column keeps verbatim.
func TestConformance_ObjectParamsBindWithoutHTMLEscaping(t *testing.T) {
	for _, mode := range []struct {
		name    string
		headers map[string]string
	}{
		{"native", nil},
		{"raw text", driverHeaders},
	} {
		for _, tc := range []struct {
			name  string
			cast  string
			oid   uint32
			param string
			want  string
		}{
			{"object", "json", oidJSON, `{"z":"<b>&</b>","a":1}`, `{"a":1,"z":"<b>&</b>"}`},
			{"object into text", "text", 25, `{"html":"<p>"}`, `{"html":"<p>"}`},
			{"object in array", "json[]", 199, `[{"k":"a&b"}]`, `{"{\"k\":\"a&b\"}"}`},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				query := "SELECT $1::" + tc.cast + " AS v"
				pg := pgfake.New(t)
				pg.SetResult(query, pgfake.Result{
					Cols:      []pgfake.Col{{Name: "v", TypeOID: 25}},
					Rows:      [][]any{{"x"}},
					Tag:       "SELECT 1",
					ParamOIDs: []uint32{tc.oid},
				})
				e := newConformanceServer(t, pg)

				rec := doSQL(e, `{"query":"`+query+`","params":[`+tc.param+`]}`, mode.headers)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.Equal(t, [][]byte{[]byte(tc.want)}, pg.LastBindParams())
			})
		}
	}
}
