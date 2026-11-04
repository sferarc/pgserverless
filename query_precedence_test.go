package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Neon parses the body as a single statement before a batch, so a string query
// wins and queries is skipped unread, even when its items would be refused.
func TestConformance_StringQueryWinsOverQueries(t *testing.T) {
	for name, body := range map[string]string{
		"query first":        `{"query":"SELECT 1","queries":[{"query":"SELECT 2"}]}`,
		"queries first":      `{"queries":[{"query":"SELECT 2"}],"query":"SELECT 1"}`,
		"empty queries":      `{"query":"SELECT 1","queries":[]}`,
		"item with no query": `{"query":"SELECT 1","queries":[{"params":[]}]}`,
		"item of wrong type": `{"query":"SELECT 1","queries":[{"query":2}]}`,
		"queries not a list": `{"query":"SELECT 1","queries":"SELECT 2"}`,
	} {
		for _, tc := range []struct {
			headers map[string]string
			rows    string
		}{
			{nil, `[{"n":1}]`},
			{driverHeaders, `[["1"]]`},
		} {
			t.Run(name, func(t *testing.T) {
				pg := pgfake.New(t)
				pg.SetResult("SELECT 1", oneInt(1))
				pg.SetResult("SELECT 2", oneInt(2))
				e := newConformanceServer(t, pg)

				rec := doSQL(e, body, tc.headers)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var got struct {
					Rows    json.RawMessage `json:"rows"`
					Results json.RawMessage `json:"results"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got), rec.Body.String())
				assert.Nil(t, got.Results, "a string query must not run as a batch")
				assert.JSONEq(t, tc.rows, string(got.Rows))
			})
		}
	}
}

// Neon falls back to a batch when query is not a string.
func TestConformance_MissingOrNullQueryKeepsBatchMode(t *testing.T) {
	for name, body := range map[string]string{
		"no query":   `{"queries":[{"query":"SELECT 2"}]}`,
		"null query": `{"query":null,"queries":[{"query":"SELECT 2"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 2", oneInt(2))
			e := newConformanceServer(t, pg)

			rec := doSQL(e, body, driverHeaders)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), `"results":[{`)
			assert.Contains(t, rec.Body.String(), `"rows":[["2"]]`)
		})
	}
}

func TestConformance_NullQueriesIsAMissingQuery(t *testing.T) {
	e := newConformanceServer(t, pgfake.New(t))

	rec := doSQL(e, `{"queries":null}`, driverHeaders)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, ErrMissingQuery.Error(), decodeError(t, rec).Message)
}

func TestConformance_BatchItemsOfTheWrongTypeAreRefused(t *testing.T) {
	pg := pgfake.New(t)
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":2}]}`, driverHeaders)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, ErrInvalidRequest.Error(), decodeError(t, rec).Message)
}
