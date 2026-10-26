package pgserverless

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func newStdlibConformanceServer(pg *pgfake.PG) http.Handler {
	static := StaticHTTPUpstream(pg.Addr())
	return NewHTTPHandler(func(r *http.Request) (Upstream, error) {
		up, err := static(r)
		if err == nil {
			up.InsecureSkipVerify = true
		}
		return up, err
	})
}

func newEchoServer(admit Admission) http.Handler {
	e := echo.New()
	NewHandler(admit).Register(e)
	return e
}

func TestHTTPHandlerMatchesEcho(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT id FROM users", pgfake.Result{
		Cols: []pgfake.Col{{Name: "id", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	refuse := func(err error) (Admission, HTTPAdmission) {
		return func(echo.Context) (Upstream, error) { return Upstream{}, err },
			func(*http.Request) (Upstream, error) { return Upstream{}, err }
	}
	okEcho, okHTTP := func() (Admission, HTTPAdmission) {
		up := Upstream{Addr: pg.Addr(), InsecureSkipVerify: true, User: "u", Database: "db"}
		return func(echo.Context) (Upstream, error) { return up, nil },
			func(*http.Request) (Upstream, error) { return up, nil }
	}()
	badEcho, badHTTP := refuse(assert.AnError)
	deniedEcho, deniedHTTP := refuse(&AdmissionError{Status: http.StatusForbidden, Message: "suspended"})

	cases := []struct {
		name     string
		echo     Admission
		http     HTTPAdmission
		body     string
		headers  map[string]string
		wantCode int
	}{
		{"single query", okEcho, okHTTP, `{"query":"SELECT id FROM users"}`, nil, 200},
		{"array mode header", okEcho, okHTTP, `{"query":"SELECT id FROM users"}`, map[string]string{"Neon-Array-Mode": "true"}, 200},
		{"batch", okEcho, okHTTP, `{"queries":[{"query":"SELECT id FROM users"}]}`, nil, 200},
		{"invalid json", okEcho, okHTTP, `{`, nil, 400},
		{"missing query", okEcho, okHTTP, `{}`, nil, 400},
		{"batch too large", okEcho, okHTTP, `{"queries":[` + strings.Repeat(`{"query":"x"},`, MaxBatchQueries) + `{"query":"x"}]}`, nil, 400},
		{"bad isolation level", okEcho, okHTTP, `{"queries":[{"query":"SELECT 1"}]}`, map[string]string{"Neon-Batch-Isolation-Level": "bogus"}, 400},
		{"admission refusal", badEcho, badHTTP, `{"query":"SELECT 1"}`, nil, 400},
		{"admission status", deniedEcho, deniedHTTP, `{"query":"SELECT 1"}`, nil, 403},
		{"unreachable upstream", func(echo.Context) (Upstream, error) { return Upstream{Addr: "127.0.0.1:1"}, nil },
			func(*http.Request) (Upstream, error) { return Upstream{Addr: "127.0.0.1:1"}, nil },
			`{"query":"SELECT 1"}`, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viaEcho := doSQL(newEchoServer(tc.echo), tc.body, tc.headers)
			viaHTTP := doSQL(NewHTTPHandler(tc.http), tc.body, tc.headers)
			if tc.wantCode != 0 {
				require.Equal(t, tc.wantCode, viaHTTP.Code)
			}
			assert.Equal(t, viaEcho.Code, viaHTTP.Code)
			assert.Equal(t, viaEcho.Header().Get("Content-Type"), viaHTTP.Header().Get("Content-Type"))
			assert.Equal(t, viaEcho.Body.String(), viaHTTP.Body.String())
		})
	}
}

func TestHTTPHandlerRoutes(t *testing.T) {
	h := NewHTTPHandler(func(*http.Request) (Upstream, error) { return Upstream{}, assert.AnError })
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/sql", http.StatusMethodNotAllowed},
		{http.MethodPost, "/v2", http.StatusMethodNotAllowed},
		{http.MethodPost, "/other", http.StatusNotFound},
		{http.MethodPost, "/sql", http.StatusBadRequest},
		{http.MethodGet, WebSocketPath, http.StatusBadRequest},
		{http.MethodHead, WebSocketPath, http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
		assert.Equal(t, tc.want, rec.Code, "%s %s", tc.method, tc.path)
	}
}

func TestHTTPDialErrorHook(t *testing.T) {
	var hooked error
	var seen *http.Request
	h := NewHTTPHandler(
		func(*http.Request) (Upstream, error) { return Upstream{Addr: "127.0.0.1:1"}, nil },
		WithHTTPDialErrorHook(func(r *http.Request, err error) { seen, hooked = r, err }),
	)
	rec := doSQL(h, `{"query":"SELECT 1"}`, nil)
	require.NotEqual(t, http.StatusOK, rec.Code)
	require.Error(t, hooked)
	require.NotNil(t, seen)
}

func TestNormalizeHTTPHeaders(t *testing.T) {
	var got http.Header
	h := NormalizeHTTPHeaders(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header }))
	req := httptest.NewRequest(http.MethodPost, "/sql", nil)
	req.Header.Set("Neon-Connection-String", "neon")
	req.Header.Set("Neon-Array-Mode", "true")
	req.Header.Set("Array-Mode", "false")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "neon", got.Get("Connection-String"))
	assert.Equal(t, "false", got.Get("Array-Mode"))
}
