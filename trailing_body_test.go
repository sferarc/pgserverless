package pgserverless

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func TestConformance_BodyWithDataAfterItsJSONIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"a second request":   `{"query":"SELECT 1"}{"query":"SELECT 2"}`,
		"a second batch":     `{"queries":[{"query":"SELECT 1"}]} {"queries":[]}`,
		"a stray character":  `{"query":"SELECT 1"}x`,
		"a closing bracket":  `{"query":"SELECT 1"}]`,
		"a bare number":      `{"query":"SELECT 1"} 1`,
		"after a newline":    "{\"query\":\"SELECT 1\"}\n{\"query\":\"SELECT 2\"}",
		"an unclosed object": `{"query":"SELECT 1"}{`,
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

func TestConformance_BodyWithTrailingWhitespaceRuns(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, "{\"query\":\"SELECT 1\"} \r\n\t\n", driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"rows":[["1"]]`)
}

func TestConformance_BodyPastTheLimitAfterItsJSONIsTooLarge(t *testing.T) {
	e := newConformanceServer(t, pgfake.New(t))
	body := `{"query":"SELECT 1"}` + strings.Repeat(" ", MaxRequestBodyBytes)

	rec := doSQL(e, body, driverHeaders)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
}
