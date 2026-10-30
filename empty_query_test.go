package pgserverless

import (
	"encoding/json"
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Neon renders PostgreSQL's EmptyQueryResponse as a 200 with no fields, no rows,
// an empty command and a null rowCount.
const (
	emptyObjectResult = `{"fields":[],"command":"","rowCount":null,"rows":[],"rowAsArray":false}`
	emptyArrayResult  = `{"fields":[],"command":"","rowCount":null,"rows":[],"rowAsArray":true}`
)

func TestConformance_EmptyQuery(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		headers     map[string]string
		want        string
	}{
		{"native", "", nil, emptyObjectResult},
		{"raw text", "", driverHeaders, emptyArrayResult},
		{"whitespace on the extended protocol", "  \n", nil, emptyObjectResult},
		{"whitespace in raw text", "  \n", driverHeaders, emptyArrayResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newConformanceServer(t, pgfake.New(t))
			body, err := json.Marshal(SQLRequest{Query: &tc.query})
			require.NoError(t, err)

			rec := doSQL(e, string(body), tc.headers)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.JSONEq(t, tc.want, rec.Body.String())
		})
	}
}

func TestConformance_EmptyQueryWithParamsIsBindError(t *testing.T) {
	for _, headers := range []map[string]string{nil, driverHeaders} {
		e := newConformanceServer(t, pgfake.New(t))

		rec := doSQL(e, `{"query":"","params":[1]}`, headers)

		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		resp := decodeError(t, rec)
		require.NotNil(t, resp.Code)
		assert.Equal(t, "08P01", *resp.Code)
		assert.Equal(t, `bind message supplies 1 parameters, but prepared statement "" requires 0`, resp.Message)
	}
}

func TestConformance_NullQueryStillRejected(t *testing.T) {
	e := newConformanceServer(t, pgfake.New(t))

	rec := doSQL(e, `{"query":null}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrMissingQuery.Error(), decodeError(t, rec).Message)
}

// An empty item keeps its place in every batch path, so the items after it
// still get their own results.
func TestConformance_EmptyQueryInBatch(t *testing.T) {
	txn := map[string]string{"Neon-Batch-Isolation-Level": "Serializable"}
	for _, tc := range []struct {
		name    string
		headers map[string]string
		first   string
		two     any
	}{
		{"native", nil, emptyObjectResult, map[string]any{"n": float64(2)}},
		{"raw text", driverHeaders, emptyArrayResult, []any{"2"}},
		{"raw text in a transaction", merge(driverHeaders, txn), emptyArrayResult, []any{"2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 2 AS n", oneInt(2))
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"queries":[{"query":""},{"query":"SELECT 2 AS n"},{"query":"","arrayMode":true}]}`, tc.headers)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var resp struct{ Results []json.RawMessage }
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Results, 3)
			assert.JSONEq(t, tc.first, string(resp.Results[0]))
			assert.JSONEq(t, emptyArrayResult, string(resp.Results[2]))
			var two struct{ Rows []any }
			require.NoError(t, json.Unmarshal(resp.Results[1], &two))
			assert.Equal(t, []any{tc.two}, two.Rows)
		})
	}
}

// The simple protocol skips an empty statement inside a joined query, so a
// governed batch answers an empty item itself and sends only the others.
func TestConformance_Governed_EmptyQuery(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 2 AS n", oneInt(2))
	e := newGovernedConformanceServer(pg, func(sql string) (int, error) {
		if sql == "-- nothing" {
			return 0, nil
		}
		return semicolonCount(sql)
	})

	t.Run("single", func(t *testing.T) {
		rec := doSQL(e, `{"query":""}`, driverHeaders)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, emptyArrayResult, rec.Body.String())
	})

	t.Run("single with params", func(t *testing.T) {
		rec := doSQL(e, `{"query":"-- nothing","params":[1]}`, driverHeaders)

		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		resp := decodeError(t, rec)
		require.NotNil(t, resp.Code)
		assert.Equal(t, "08P01", *resp.Code)
	})

	t.Run("batch", func(t *testing.T) {
		before := len(pg.SimpleSQL())
		rec := doSQL(e, `{"queries":[{"query":"-- nothing"},{"query":"SELECT 2 AS n"},{"query":""}]}`, driverHeaders)

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp BatchResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Results, 3)
		for _, i := range []int{0, 2} {
			got, err := json.Marshal(resp.Results[i])
			require.NoError(t, err)
			assert.JSONEq(t, emptyArrayResult, string(got))
		}
		assert.Equal(t, []any{[]any{"2"}}, resp.Results[1].Rows)
		assert.Equal(t, []string{"\nSELECT 2 AS n\n"}, pg.SimpleSQL()[before:])
	})

	t.Run("batch item with params", func(t *testing.T) {
		before := len(pg.SimpleSQL())
		rec := doSQL(e, `{"queries":[{"query":"SELECT 2 AS n"},{"query":"-- nothing","params":[1]}]}`, driverHeaders)

		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		resp := decodeError(t, rec)
		require.NotNil(t, resp.Code)
		assert.Equal(t, "08P01", *resp.Code)
		assert.Empty(t, pg.SimpleSQL()[before:])
	})
}

func merge(a, b map[string]string) map[string]string {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}
