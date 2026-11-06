package pgserverless

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Neon's serde reads members by exact name, so a differently cased one is an unknown member it ignores.
func TestConformance_BodyMemberNamesAreCaseSensitive(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		rows string
	}{
		"QUERY after query":       {`{"query":"SELECT 1","QUERY":"SELECT 2"}`, `"rows":[["1"]]`},
		"Query before query":      {`{"Query":"SELECT 2","query":"SELECT 1"}`, `"rows":[["1"]]`},
		"Params ignored":          {`{"query":"SELECT 1","Params":[1]}`, `"rows":[["1"]]`},
		"QUERIES beside a string": {`{"query":"SELECT 1","QUERIES":[{"query":"SELECT 2"}]}`, `"rows":[["1"]]`},
		"Query in a batch item":   {`{"queries":[{"query":"SELECT 1","Query":"SELECT 2"}]}`, `"results":[{`},
	} {
		t.Run(name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1", oneInt(1))
			pg.SetResult("SELECT 2", oneInt(2))
			e := newConformanceServer(t, pg)

			rec := doSQL(e, tc.body, driverHeaders)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.rows)
			assert.Contains(t, rec.Body.String(), `"rows":[["1"]]`)
			assert.NotContains(t, rec.Body.String(), `"2"`)
		})
	}
}

func TestConformance_BodyWithOnlyADifferentlyCasedQueryIsAMissingQuery(t *testing.T) {
	pg := pgfake.New(t)
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"Query":"SELECT 1"}`, driverHeaders)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, ErrMissingQuery.Error(), decodeError(t, rec).Message)
	assert.Empty(t, pg.StartupParam("user"), "a refused body must not reach the database")
}

// serde refuses a repeated field, where encoding/json keeps the last one.
func TestConformance_BodyWithARepeatedMemberIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"query":                `{"query":"SELECT 1","query":"SELECT 2"}`,
		"params":               `{"query":"SELECT $1","params":[1],"params":[2]}`,
		"arrayMode":            `{"query":"SELECT 1","arrayMode":true,"arrayMode":false}`,
		"queries":              `{"queries":[{"query":"SELECT 1"}],"queries":[{"query":"SELECT 2"}]}`,
		"query in an item":     `{"queries":[{"query":"SELECT 1","query":"SELECT 2"}]}`,
		"params in an item":    `{"queries":[{"query":"SELECT $1","params":[1],"params":[2]}]}`,
		"arrayMode in an item": `{"queries":[{"query":"SELECT 1","arrayMode":true,"arrayMode":true}]}`,
		"arrayMode by a batch": `{"queries":[{"query":"SELECT 1"}],"arrayMode":true,"arrayMode":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			pg := pgfake.New(t)
			e := newConformanceServer(t, pg)

			rec := doSQL(e, body, driverHeaders)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Equal(t, ErrInvalidRequest.Error(), decodeError(t, rec).Message)
			assert.Empty(t, pg.StartupParam("user"), "a refused body must not reach the database")
		})
	}
}

// Neon's untagged Payload tries the batch shape once the single one fails, so queries still runs.
func TestConformance_InvalidSingleShapeFallsBackToQueries(t *testing.T) {
	for name, body := range map[string]string{
		"repeated query":  `{"query":"SELECT 1","query":"SELECT 1","queries":[{"query":"SELECT 2"}]}`,
		"query not text":  `{"query":2,"queries":[{"query":"SELECT 2"}]}`,
		"params not list": `{"query":"SELECT 1","params":"x","queries":[{"query":"SELECT 2"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1", oneInt(1))
			pg.SetResult("SELECT 2", oneInt(2))
			e := newConformanceServer(t, pg)

			rec := doSQL(e, body, driverHeaders)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), `"results":[{`)
			assert.Contains(t, rec.Body.String(), `"rows":[["2"]]`)
		})
	}
}

// A repeated member Neon does not know is ignored like any other unknown member.
func TestConformance_BodyWithARepeatedUnknownMemberRuns(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 1","note":1,"note":2,"queries":1,"queries":2}`, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"rows":[["1"]]`)
}
