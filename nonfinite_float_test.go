package pgserverless

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// NaN, Infinity and -Infinity are ordinary float4 and float8 values that any
// customer table can hold, but encoding/json refuses to marshal them. Before
// coerceNeonValue handled them, one such value anywhere in a result set failed
// the whole response with a 500 and took every other row and column with it, so
// a single bad reading in a metrics table made an entire query unreadable over
// the serverless endpoint. They must come back as PostgreSQL's own text, which
// is what the endpoint's raw-text mode already returns for them.
func TestConformance_NonFiniteFloatsAsStrings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{"float8 NaN", oidFloat8, math.NaN(), `"NaN"`},
		{"float8 Infinity", oidFloat8, math.Inf(1), `"Infinity"`},
		{"float8 -Infinity", oidFloat8, math.Inf(-1), `"-Infinity"`},
		{"float4 NaN", oidFloat4, float32(math.NaN()), `"NaN"`},
		{"float4 Infinity", oidFloat4, float32(math.Inf(1)), `"Infinity"`},
		{"float8 array with NaN", oidFloat8Array, []float64{1.5, math.NaN()}, `[1.5,"NaN"]`},
		{"float4 array with Infinity", oidFloat4Array, []float32{1.5, float32(math.Inf(1))}, `[1.5,"Infinity"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT f FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "f", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT f FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["f"]))
		})
	}
}

// The coercion is confined to the values encoding/json cannot represent. A
// finite float stays a JSON number, so no client that already reads these
// columns as numbers has to change.
func TestConformance_FiniteFloatsStayNumbers(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT f, g FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "f", TypeOID: oidFloat8},
			{Name: "g", TypeOID: oidFloat8Array},
		},
		Rows: [][]any{{1.5, []float64{2.5, -3.25}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT f, g FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `1.5`, string(rows[0]["f"]))
	assert.JSONEq(t, `[2.5,-3.25]`, string(rows[0]["g"]))
}

// Raw-text mode is the driver's own primary path and never reaches
// coerceNeonValue, so it was never broken: it already returned PostgreSQL's text
// for these values. This pins the spelling the JSON path was made to agree with,
// so the two paths cannot drift apart again.
func TestConformance_NonFiniteFloatsRawTextUnchanged(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT f FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "f", TypeOID: oidFloat8}},
		Rows: [][]any{{math.NaN()}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT f FROM t"}`, map[string]string{
		"Neon-Raw-Text-Output": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"NaN"`, string(rows[0]["f"]))
}

// A non-finite float sent as a query parameter comes back as the same value.
// formatPgArrayFloat already wrote these spellings on the request side, so
// fixing only the response side would have left the endpoint able to accept a
// value it could not return.
func TestNonFiniteFloatRoundTripsSpelling(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
		want  string
	}{
		{"NaN", math.NaN(), "NaN"},
		{"Infinity", math.Inf(1), "Infinity"},
		{"-Infinity", math.Inf(-1), "-Infinity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nonFiniteFloatString(tc.value)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)

			// The array literal the request side writes quotes the same
			// spelling, so what goes in is what comes back out.
			assert.Equal(t, `"`+tc.want+`"`, formatPgArrayFloat(tc.value))
		})
	}
}

// Every finite value, and every type that is not a float, is left alone.
func TestNonFiniteFloatStringIgnoresEverythingElse(t *testing.T) {
	for _, v := range []any{
		0.0, 1.5, -1.5, math.MaxFloat64, -math.MaxFloat64,
		float32(0), float32(1.5),
		int64(1), "NaN", nil, []any{math.NaN()},
	} {
		_, ok := nonFiniteFloatString(v)
		assert.False(t, ok, "%#v must not be coerced", v)
	}
}
