package pgserverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/labstack/echo/v4"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cancelServers(pg *pgfake.PG) map[string]http.Handler {
	static := StaticUpstream(pg.Addr())
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		return up, err
	}).Register(e)

	staticHTTP := StaticHTTPUpstream(pg.Addr())
	std := NewHTTPHandler(func(r *http.Request) (Upstream, error) {
		up, err := staticHTTP(r)
		up.InsecureSkipVerify = true
		return up, err
	})

	return map[string]http.Handler{"echo": e, "net/http": std}
}

// doSQLUntil serves body with a request context that ends after d, as a query timeout or a departed client ends it.
func doSQLUntil(t *testing.T, h http.Handler, body string, headers map[string]string, d time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/sql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEndedRequestCancelsTheStatementOnTheUpstream(t *testing.T) {
	cases := map[string]struct {
		body    string
		headers map[string]string
	}{
		"single native":   {`{"query":"SELECT pg_sleep(60)"}`, nil},
		"single raw text": {`{"query":"SELECT pg_sleep(60)"}`, map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"}},
		"batch":           {`{"queries":[{"query":"SELECT 1"},{"query":"SELECT pg_sleep(60)"}]}`, nil},
		"batch with options": {
			`{"queries":[{"query":"SELECT 1"},{"query":"SELECT pg_sleep(60)"}]}`,
			map[string]string{"Neon-Batch-Read-Only": "true"},
		},
	}
	for caseName, tc := range cases {
		pg := pgfake.New(t)
		pg.SetResult("SELECT 1", oneInt(1))
		pg.SetResult("SELECT pg_sleep(60)", pgfake.Result{WaitForCancel: true})

		for name, h := range cancelServers(pg) {
			t.Run(caseName+"/"+name, func(t *testing.T) {
				before := pg.CancelRequests()
				start := time.Now()
				rec := doSQLUntil(t, h, tc.body, tc.headers, 200*time.Millisecond)

				assert.Less(t, time.Since(start), 5*time.Second)
				assert.Equal(t, before+1, pg.CancelRequests(), "the statement was left running on the upstream")
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				resp := decodeError(t, rec)
				assert.Equal(t, "Query cancelled, connection was terminated", resp.Message)
				require.NotNil(t, resp.Code)
				assert.Equal(t, "08P01", *resp.Code)
			})
		}
	}
}

func TestStatementTimeoutErrorPassesThroughWhileTheRequestIsLive(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT pg_sleep(60)", pgfake.Result{PgError: &pgproto3.ErrorResponse{
		Severity: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout",
	}})

	for name, h := range cancelServers(pg) {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT pg_sleep(60)"}`, nil)

			resp := decodeError(t, rec)
			assert.Equal(t, "canceling statement due to statement timeout", resp.Message)
			require.NotNil(t, resp.Code)
			assert.Equal(t, "57014", *resp.Code)
			assert.Zero(t, pg.CancelRequests())
		})
	}
}
