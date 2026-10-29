package pgserverless

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Neon writes an object-mode row's keys in column order; a Go map sorted them.
func TestConformance_ObjectRowKeepsColumnOrder(t *testing.T) {
	const query = "SELECT 2 AS b, 'x' AS a, NULL AS c, 1 AS b"
	for _, mode := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"native", nil, `{"b":2,"a":"x","c":null,"b":1}`},
		{"raw text", map[string]string{"Raw-Text-Output": "true"}, `{"b":"2","a":"x","c":null,"b":"1"}`},
	} {
		t.Run(mode.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult(query, pgfake.Result{
				Cols: []pgfake.Col{
					{Name: "b", TypeOID: oidInt4},
					{Name: "a", TypeOID: oidText},
					{Name: "c", TypeOID: oidText},
					{Name: "b", TypeOID: oidInt4},
				},
				Rows: [][]any{{int32(2), "x", nil, int32(1)}},
				Tag:  "SELECT 1",
			})

			rec := doSQL(newConformanceServer(t, pg), `{"query":"`+query+`"}`, mode.headers)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var resp struct {
				Rows []json.RawMessage `json:"rows"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Rows, 1)
			assert.Equal(t, mode.want, string(resp.Rows[0]))
		})
	}
}

func TestObjectRowMarshalJSON(t *testing.T) {
	got, err := json.Marshal(ObjectRow{Names: []string{"z", "a<b"}, Values: []any{[]any{1, "two"}, json.RawMessage(`{"k":1}`)}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"z":[1,"two"],"a<b":{"k":1}}`, string(got))
	assert.Less(t, strings.Index(string(got), `"z"`), strings.Index(string(got), `"a`))

	got, err = json.Marshal(ObjectRow{})
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(got))
}
