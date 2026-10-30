package pgserverless

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	return resp
}

func oneInt(n int32) pgfake.Result {
	return pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{n}},
		Tag:  "SELECT 1",
	}
}

func TestBatchFailureReturnsTheErrorAndNoPartialResults(t *testing.T) {
	failing := pgfake.Result{PgError: &pgproto3.ErrorResponse{
		Severity: "ERROR", Code: "23505", Message: "duplicate key", ConstraintName: "users_pkey",
	}}
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"implicit transaction", nil},
		{"explicit transaction", map[string]string{"Neon-Batch-Isolation-Level": "ReadCommitted"}},
		{"raw text", map[string]string{"Neon-Raw-Text-Output": "true"}},
		{"raw text, explicit transaction", map[string]string{"Neon-Raw-Text-Output": "true", "Neon-Batch-Read-Only": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1 AS n", oneInt(1))
			pg.SetResult("INSERT INTO users VALUES (1)", failing)
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"queries":[{"query":"SELECT 1 AS n"},{"query":"INSERT INTO users VALUES (1)"}]}`, tc.headers)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			resp := decodeError(t, rec)
			require.NotNil(t, resp.Code)
			assert.Equal(t, "23505", *resp.Code)
			require.NotNil(t, resp.ConstraintName)
			assert.Equal(t, "users_pkey", *resp.ConstraintName)
			assert.NotContains(t, rec.Body.String(), `"results"`)
		})
	}
}

func TestBatchTransactionBeginCarriesEveryOption(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS n", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1 AS n"}]}`, map[string]string{
		"Neon-Batch-Isolation-Level": "Serializable",
		"Neon-Batch-Read-Only":       "true",
		"Neon-Batch-Deferrable":      "true",
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", pg.LastBegin())
}

func TestBatchOverTheLimitIsRejectedBeforeDialing(t *testing.T) {
	// No backend is running: a dial would surface as a 504, not a 400.
	h := NewHandler(StaticUpstream("127.0.0.1:1"))
	e := echo.New()
	h.Register(e)

	items := strings.TrimSuffix(strings.Repeat(`{"query":"select 1"},`, MaxBatchQueries+1), ",")
	rec := doSQL(e, `{"queries":[`+items+`]}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, decodeError(t, rec).Message, fmt.Sprintf("max %d", MaxBatchQueries))
}

func TestBatchAtTheLimitRuns(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS n", oneInt(1))
	e := newConformanceServer(t, pg)

	items := strings.TrimSuffix(strings.Repeat(`{"query":"SELECT 1 AS n"},`, MaxBatchQueries), ",")
	rec := doSQL(e, `{"queries":[`+items+`]}`, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Len(t, out.Results, MaxBatchQueries)
}

// paddedBody left-pads query JSON with whitespace, which the decoder skips, to exactly n bytes.
func paddedBody(n int) string {
	const body = `{"query":"SELECT 1 AS n"}`
	return strings.Repeat(" ", n-len(body)) + body
}

func TestBodyOverTheLimitIsRejectedBeforeDialing(t *testing.T) {
	// No backend is running: a dial would surface as a 504, not a 413.
	for name, h := range map[string]http.Handler{
		"echo":     newEchoServer(StaticUpstream("127.0.0.1:1")),
		"net/http": NewHTTPHandler(StaticHTTPUpstream("127.0.0.1:1")),
	} {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, paddedBody(MaxRequestBodyBytes+1), nil)

			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
			assert.Equal(t, fmt.Sprintf("request is too large (max is %d bytes)", MaxRequestBodyBytes), decodeError(t, rec).Message)
		})
	}
}

func TestBodyAtTheLimitRuns(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS n", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, paddedBody(MaxRequestBodyBytes), nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestParameterCountMismatchIsAProtocolViolation(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT $1::int AS n", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT $1::int AS n","params":[]}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "08P01", *resp.Code)
}

func TestUnknownStatementIsAnError(t *testing.T) {
	e := newConformanceServer(t, pgfake.New(t))

	rec := doSQL(e, `{"query":"SELECT * FROM missing"}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "42P01", *resp.Code)
}

func TestRawTextSingleQueryError(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT boom", pgfake.Result{PgError: &pgproto3.ErrorResponse{Severity: "ERROR", Code: "22012", Message: "division by zero"}})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT boom"}`, map[string]string{"Neon-Raw-Text-Output": "true"})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "division by zero", decodeError(t, rec).Message)
}

func TestAdmissionRefusalOnSQLUsesItsStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"typed", &AdmissionError{Status: http.StatusNotFound, Message: "unknown host"}, http.StatusNotFound},
		{"plain", fmt.Errorf("nope"), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			NewHandler(func(echo.Context) (Upstream, error) { return Upstream{}, tc.err }).Register(e)

			rec := doSQL(e, `{"query":"select 1"}`, nil)

			assert.Equal(t, tc.want, rec.Code)
			assert.Equal(t, tc.err.Error(), decodeError(t, rec).Message)
		})
	}
}

func TestUnreadableTransactionHeaderIsRejectedBeforeDialing(t *testing.T) {
	e := echo.New()
	NewHandler(StaticUpstream("127.0.0.1:1")).Register(e)

	rec := doSQL(e, `{"queries":[{"query":"select 1"}]}`, map[string]string{"Neon-Batch-Deferrable": "maybe"})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, decodeError(t, rec).Message, "Batch-Deferrable")
}

func TestApplicationNameReachesTheUpstream(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS n", oneInt(1))
	static := StaticUpstream(pg.Addr())
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		up.AppName = "pgserverless-test"
		return up, err
	}).Register(e)

	rec := doSQL(e, `{"query":"SELECT 1 AS n"}`, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "pgserverless-test", pg.StartupParam("application_name"))
}

func TestRejectedCredentialsAre400AndReachTheDialHook(t *testing.T) {
	pg := pgfake.NewRejectingAuth(t, "28P01", "password authentication failed")
	static := StaticUpstream(pg.Addr())
	var hooked error
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		return up, err
	}, WithDialErrorHook(func(_ echo.Context, err error) { hooked = err })).Register(e)

	rec := doSQL(e, `{"query":"SELECT 1"}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Error(t, hooked)
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "28P01", *resp.Code)
}

func TestDialToAnUnreachableUpstreamIs500WithoutLeakingTheAddress(t *testing.T) {
	e := echo.New()
	NewHandler(StaticUpstream("127.0.0.1:1")).Register(e)

	rec := doSQL(e, `{"query":"select 1"}`, nil)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "127.0.0.1")
	assert.NotContains(t, rec.Body.String(), "p@")
}

func TestIsolationLevelSpellings(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{"", "", false},
		{"ReadUncommitted", "READ UNCOMMITTED", false},
		{"read committed", "READ COMMITTED", false},
		{"REPEATABLE_READ", "REPEATABLE READ", false},
		{" serializable ", "SERIALIZABLE", false},
		{"read-committed", "READ COMMITTED", false},
		{"snapshot", "", true},
		{"serializable; drop table users", "", true},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := isolationLevelSQL(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBeginSQLOrdering(t *testing.T) {
	for _, tc := range []struct {
		opts txnOptions
		want string
	}{
		{txnOptions{}, "BEGIN"},
		{txnOptions{deferrable: true}, "BEGIN DEFERRABLE"},
		{txnOptions{readOnly: true, deferrable: true}, "BEGIN READ ONLY DEFERRABLE"},
		{txnOptions{isolationLevel: "REPEATABLE READ"}, "BEGIN ISOLATION LEVEL REPEATABLE READ"},
	} {
		assert.Equal(t, tc.want, tc.opts.beginSQL())
		assert.Equal(t, tc.want == "BEGIN", tc.opts.isDefault())
	}
}
