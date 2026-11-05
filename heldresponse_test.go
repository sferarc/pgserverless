package pgserverless

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"

	"github.com/sferarc/pgserverless/pgfake"
)

// stalledReader blocks the first body write until release is closed, as a client that stops reading does.
type stalledReader struct {
	*httptest.ResponseRecorder
	writing chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *stalledReader) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.writing) })
	<-s.release
	return s.ResponseRecorder.Write(p)
}

func TestSQLClosesTheUpstreamBeforeWritingTheResponse(t *testing.T) {
	readOnly := map[string]string{"Neon-Batch-Read-Only": "true"}
	cases := map[string]struct {
		body    string
		headers map[string]string
		status  int
		want    string
	}{
		"single native":       {`{"query":"SELECT 1 AS v"}`, nil, http.StatusOK, `"rows":[{"v":1}]`},
		"single raw text":     {`{"query":"SELECT 1 AS v"}`, driverHeaders, http.StatusOK, `"rows":[["1"]]`},
		"batch native":        {`{"queries":[{"query":"SELECT 1 AS v"},{"query":"SELECT 1 AS v"}]}`, nil, http.StatusOK, `"results":[`},
		"batch raw text":      {`{"queries":[{"query":"SELECT 1 AS v"},{"query":"SELECT 1 AS v"}]}`, driverHeaders, http.StatusOK, `"results":[`},
		"batch read only":     {`{"queries":[{"query":"SELECT 1 AS v"}]}`, readOnly, http.StatusOK, `"results":[`},
		"batch with an error": {`{"queries":[{"query":"SELECT 1 AS v"},{"query":"SELECT boom"}]}`, nil, http.StatusBadRequest, `"code":"42703"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1 AS v", pgfake.Result{
				Cols: []pgfake.Col{{Name: "v", TypeOID: oidInt4}},
				Rows: [][]any{{int32(1)}},
				Tag:  "SELECT 1",
			})
			pg.SetResult("SELECT boom", pgfake.Result{PgError: &pgproto3.ErrorResponse{
				Severity: "ERROR", Code: "42703", Message: `column "boom" does not exist`,
			}})
			h := newConformanceServer(t, pg)

			req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			w := &stalledReader{ResponseRecorder: httptest.NewRecorder(), writing: make(chan struct{}), release: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				defer close(done)
				h.ServeHTTP(w, req)
			}()

			select {
			case <-w.writing:
			case <-time.After(5 * time.Second):
				t.Fatal("the response was never written")
			}
			assert.Eventually(t, func() bool { return pg.Sessions() == 0 }, 2*time.Second, 10*time.Millisecond,
				"the upstream session stays open while the client has not read the response")
			close(w.release)
			<-done

			assert.Equal(t, tc.status, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			if tc.headers["Neon-Batch-Read-Only"] != "" {
				assert.Equal(t, "true", w.Header().Get("Neon-Batch-Read-Only"))
			}
		})
	}
}
