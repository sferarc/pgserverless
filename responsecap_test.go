package pgserverless

import (
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tenTextRows is ten rows of a ten-byte text value: 100 bytes of row values.
func tenTextRows() pgfake.Result {
	rows := make([][]any, 10)
	for i := range rows {
		rows[i] = []any{strings.Repeat("x", 10)}
	}
	return pgfake.Result{Cols: []pgfake.Col{{Name: "v", TypeOID: oidText}}, Rows: rows, Tag: "SELECT 10"}
}

func cappedServers(pg *pgfake.PG, maxBytes int64) map[string]http.Handler {
	static := StaticUpstream(pg.Addr())
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		return up, err
	}, WithMaxResponseBytes(maxBytes)).Register(e)

	staticHTTP := StaticHTTPUpstream(pg.Addr())
	std := NewHTTPHandler(func(r *http.Request) (Upstream, error) {
		up, err := staticHTTP(r)
		up.InsecureSkipVerify = true
		return up, err
	}, WithHTTPMaxResponseBytes(maxBytes))

	return map[string]http.Handler{"echo": e, "net/http": std}
}

func TestResponseOverTheCapIs507(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())

	for name, h := range cappedServers(pg, 99) {
		for mode, headers := range map[string]map[string]string{
			"native":   nil,
			"raw text": {"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 99 bytes)", decodeError(t, rec).Message)
			})
		}
	}
}

func TestResponseAtTheCapRuns(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())

	for name, h := range cappedServers(pg, 100) {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

func TestBatchStatementsShareTheResponseCap(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())
	pg.SetResult("SELECT v FROM u", tenTextRows())
	batch := `{"queries":[{"query":"SELECT v FROM t"},{"query":"SELECT v FROM u"}]}`

	for name, h := range cappedServers(pg, 150) {
		for mode, headers := range map[string]map[string]string{
			"implicit":  nil,
			"read only": {"Neon-Batch-Read-Only": "true"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQL(h, batch, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 150 bytes)", decodeError(t, rec).Message)
			})
		}
	}
}

func TestResponsesAreUncappedByDefault(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())

	for name, h := range cappedServers(pg, 0) {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}
