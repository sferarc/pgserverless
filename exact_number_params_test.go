package pgserverless

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// 9007199254740993 is 2^53 + 1, the first integer a float64 cannot hold; decoded as one it bound as ...992.
func TestConformance_IntegerParamKeepsItsDigits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		query    string
		paramOID uint32
		params   string
		want     string
	}{
		{"int8 scalar", "SELECT $1::int8 AS v", oidInt8, `[9007199254740993]`, "9007199254740993"},
		{"negative int8 scalar", "SELECT $1::int8 AS v", oidInt8, `[-9007199254740993]`, "-9007199254740993"},
		{"int8 array element", "SELECT $1::int8[] AS v", oidInt8Array, `[[9007199254740993,1e6]]`, "{9007199254740993,1000000}"},
		{"jsonb object member", "SELECT $1::jsonb AS v", oidJSONB, `[{"id":9007199254740993}]`, `{"id":9007199254740993}`},
	} {
		for _, mode := range []struct {
			name    string
			headers map[string]string
		}{
			{"native", nil},
			{"raw text", driverHeaders},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				pg := pgfake.New(t)
				pg.SetResult(tc.query, pgfake.Result{
					Cols:      []pgfake.Col{{Name: "v", TypeOID: oidText}},
					Rows:      [][]any{{"ok"}},
					Tag:       "SELECT 1",
					ParamOIDs: []uint32{tc.paramOID},
				})
				e := newConformanceServer(t, pg)

				rec := doSQL(e, `{"query":"`+tc.query+`","params":`+tc.params+`}`, mode.headers)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Len(t, pg.LastBindParams(), 1)
				assert.Equal(t, tc.want, string(pg.LastBindParams()[0]))
			})
		}
	}
}

func TestJSONNumberText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"9007199254740993", "9007199254740993"},
		{"-9007199254740993", "-9007199254740993"},
		{"99999999999999999999", "99999999999999999999"},
		{"0", "0"},
		{"1.5", "1.5"},
		{"1e6", "1000000"},
		{"1E21", "1000000000000000000000"},
		{"2.5e-3", "0.0025"},
		{"1e400", "1e400"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, jsonNumberText(json.Number(tc.in)))
		})
	}
}

// A number outside float64's range used to fail the decode and answer 400 before PostgreSQL could judge it.
func TestDecodeSQLRequest_KeepsNumberText(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"select $1, $2","params":[9007199254740993,1e400]}`))

	got, err := decodeSQLRequest(req)

	require.NoError(t, err)
	assert.Equal(t, []any{json.Number("9007199254740993"), json.Number("1e400")}, got.Params)
	assert.Equal(t, []any{"9007199254740993", "1e400"}, ConvertParams(got.Params))
}
