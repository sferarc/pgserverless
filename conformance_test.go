package pgserverless

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// mustNumericConf builds a pgtype.Numeric from its decimal text form, as pgx
// would decode it off the wire. Used by numeric-string conformance fixtures.
func mustNumericConf(s string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		panic(err)
	}
	return n
}

// This file is the Neon serverless-driver compatibility conformance suite.
// It exercises the real /sql HTTP handler end-to-end against an in-process
// PostgreSQL backend (fakePG) and asserts that the handler emits the exact
// JSON request/response contract documented by @neondatabase/serverless.
//
// Reference contract (from the driver source / docs):
//   - Single query  -> FullQueryResults JSON object.
//   - Batch (transaction) -> array of per-statement results.
//   - FullQueryResults shape: { fields, command, rowCount, rows, rowAsArray }.
//   - Array mode: rows are positional arrays; rowAsArray=true.
//   - Errors: full pg-error JSON (message, code, severity, detail, hint, ...).
//   - Headers: Neon-Connection-String / Neon-Array-Mode / Neon-Pool-Opt-In /
//     Neon-Raw-Text-Output normalize to their standard equivalents.

const (
	conformanceHost = "db.example.test"
)

// PG type OIDs used in fixtures (text==25 is not in the typed consts).
const oidText uint32 = 25

// newConformanceServer wires a real serverless Server whose /sql loopback
// points at the fake PostgreSQL backend, with one project registered for
// subdomain routing. It returns the echo engine, or the stdlib handler when PGSERVERLESS_TEST_STDLIB is true.
func newConformanceServer(t *testing.T, pg *pgfake.PG) http.Handler {
	t.Helper()
	if os.Getenv("PGSERVERLESS_TEST_STDLIB") == "true" {
		return newStdlibConformanceServer(pg)
	}
	e := echo.New()
	static := StaticUpstream(pg.Addr())
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		if err == nil {
			// pgfake uses a test-only self-signed certificate. Keep that trust
			// decision explicit in the test instead of weakening production defaults.
			up.InsecureSkipVerify = true
		}
		return up, err
	}).Register(e)
	return e
}

// doSQL issues a POST /sql with the given JSON body and headers, returning the
// recorder. The connection string targets the conformance project host.
func doSQL(e http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Single query — object mode (default)
// ---------------------------------------------------------------------------

func TestConformance_SingleQuery_ObjectMode(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT id, name FROM users", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "id", TypeOID: oidInt4},
			{Name: "name", TypeOID: oidText},
		},
		Rows: [][]any{
			{int32(1), "alice"},
			{int32(2), "bob"},
		},
		Tag: "SELECT 2",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT id, name FROM users"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))

	// FullQueryResults contract shape.
	assert.Equal(t, "SELECT", result.Command)
	require.NotNil(t, result.RowCount)
	assert.Equal(t, 2, *result.RowCount)
	assert.False(t, result.RowAsArray)
	require.Len(t, result.Fields, 2)
	assert.Equal(t, "id", result.Fields[0].Name)
	assert.Equal(t, oidInt4, result.Fields[0].DataTypeID)
	assert.Equal(t, "text", result.Fields[0].Format)

	// Object-mode Rows: keyed by column name, native types.
	require.Len(t, result.Rows, 2)
	row0, ok := result.Rows[0].(map[string]any)
	require.True(t, ok, "object mode rows must be objects")
	assert.EqualValues(t, 1, row0["id"]) // int4 -> number
	assert.Equal(t, "alice", row0["name"])
}

// ---------------------------------------------------------------------------
// Single query — array mode (Neon-Array-Mode header)
// ---------------------------------------------------------------------------

func TestConformance_SingleQuery_ArrayMode_Header(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a, 2 AS b", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "a", TypeOID: oidInt4},
			{Name: "b", TypeOID: oidInt4},
		},
		Rows: [][]any{{int32(1), int32(2)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 1 AS a, 2 AS b"}`, map[string]string{
		"Neon-Array-Mode": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))

	assert.True(t, result.RowAsArray, "Neon-Array-Mode header must set rowAsArray")
	require.Len(t, result.Rows, 1)
	row0, ok := result.Rows[0].([]any)
	require.True(t, ok, "array mode rows must be positional arrays")
	require.Len(t, row0, 2)
	assert.EqualValues(t, 1, row0[0])
	assert.EqualValues(t, 2, row0[1])
}

// Array mode can also be requested via the request body (driver sends both).
func TestConformance_SingleQuery_ArrayMode_Body(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 7 AS n", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{int32(7)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 7 AS n","arrayMode":true}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.RowAsArray)
	row0, ok := result.Rows[0].([]any)
	require.True(t, ok)
	assert.EqualValues(t, 7, row0[0])
}

// A single query's arrayMode:false beats Neon-Array-Mode:true, since Neon resolves array_mode.unwrap_or(header).
func TestConformance_SingleQuery_ArrayMode_BodyFalseOverridesHeader(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 7 AS n", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{int32(7)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 7 AS n","arrayMode":false}`, map[string]string{
		"Neon-Array-Mode": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.False(t, result.RowAsArray)
	row0, ok := result.Rows[0].(map[string]any)
	require.True(t, ok, "body arrayMode:false must return object rows")
	assert.EqualValues(t, 7, row0["n"])
}

// A batch has no top-level arrayMode in Neon; ours only turns array mode on, so false leaves the header in charge.
func TestConformance_BatchTopLevelArrayModeFalseKeepsHeader(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1 AS a"}],"arrayMode":false}`, map[string]string{
		"Neon-Array-Mode": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 1)
	assert.True(t, batch.Results[0].RowAsArray)
}

// ---------------------------------------------------------------------------
// Empty result set — rows must be [] (never null), as the driver expects.
// ---------------------------------------------------------------------------

func TestConformance_EmptyResultSet(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT id FROM users WHERE false", pgfake.Result{
		Cols: []pgfake.Col{{Name: "id", TypeOID: oidInt4}},
		Rows: nil,
		Tag:  "SELECT 0",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT id FROM users WHERE false"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// rows must be present and an empty array, not null.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Contains(t, raw, "rows")
	assert.Equal(t, "[]", string(raw["rows"]))
	require.Contains(t, raw, "rowCount")
}

// ---------------------------------------------------------------------------
// Batch (sql.transaction([...])) — array of per-statement results.
// ---------------------------------------------------------------------------

func TestConformance_BatchTransaction(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS one", pgfake.Result{
		Cols: []pgfake.Col{{Name: "one", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	pg.SetResult("SELECT 2 AS two", pgfake.Result{
		Cols: []pgfake.Col{{Name: "two", TypeOID: oidInt4}},
		Rows: [][]any{{int32(2)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	body := `{"queries":[{"query":"SELECT 1 AS one"},{"query":"SELECT 2 AS two"}]}`
	rec := doSQL(e, body, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 2)

	r0 := batch.Results[0]
	assert.Equal(t, "SELECT", r0.Command)
	row0 := r0.Rows[0].(map[string]any)
	assert.EqualValues(t, 1, row0["one"])

	r1 := batch.Results[1]
	row1 := r1.Rows[0].(map[string]any)
	assert.EqualValues(t, 2, row1["two"])
}

// Batch honors per-query array mode.
func TestConformance_BatchTransaction_PerQueryArrayMode(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	pg.SetResult("SELECT 2 AS b", pgfake.Result{
		Cols: []pgfake.Col{{Name: "b", TypeOID: oidInt4}},
		Rows: [][]any{{int32(2)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	body := `{"queries":[{"query":"SELECT 1 AS a","arrayMode":true},{"query":"SELECT 2 AS b"}]}`
	rec := doSQL(e, body, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 2)

	// First result: array mode.
	assert.True(t, batch.Results[0].RowAsArray)
	_, ok := batch.Results[0].Rows[0].([]any)
	assert.True(t, ok, "first batch result should be array mode")

	// Second result: object mode.
	assert.False(t, batch.Results[1].RowAsArray)
	_, ok = batch.Results[1].Rows[0].(map[string]any)
	assert.True(t, ok, "second batch result should be object mode")
}

// ---------------------------------------------------------------------------
// Parameterized query — $1 placeholders with params array.
// ---------------------------------------------------------------------------

func TestConformance_ParameterizedQuery(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT $1::int AS v", pgfake.Result{
		Cols: []pgfake.Col{{Name: "v", TypeOID: oidInt4}},
		Rows: [][]any{{int32(42)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT $1::int AS v","params":[42]}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	row0 := result.Rows[0].(map[string]any)
	assert.EqualValues(t, 42, row0["v"])
}

// ---------------------------------------------------------------------------
// Error response — full pg-error JSON (NeonDbError fields).
// ---------------------------------------------------------------------------

func TestConformance_ErrorResponse_FullPgError(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("INSERT INTO users (id) VALUES (1)", pgfake.Result{
		PgError: &pgproto3.ErrorResponse{
			Severity:       "ERROR",
			Code:           "23505",
			Message:        "duplicate key value violates unique constraint \"users_pkey\"",
			Detail:         "Key (id)=(1) already exists.",
			SchemaName:     "public",
			TableName:      "users",
			ConstraintName: "users_pkey",
		},
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"INSERT INTO users (id) VALUES (1)"}`, nil)
	// Every PostgreSQL error from a statement is a 400.
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))

	assert.Contains(t, errResp.Message, "duplicate key value")
	require.NotNil(t, errResp.Code)
	assert.Equal(t, "23505", *errResp.Code)
	require.NotNil(t, errResp.Severity)
	assert.Equal(t, "ERROR", *errResp.Severity)
	require.NotNil(t, errResp.Detail)
	assert.Equal(t, "Key (id)=(1) already exists.", *errResp.Detail)
	require.NotNil(t, errResp.SchemaName)
	assert.Equal(t, "public", *errResp.SchemaName)
	require.NotNil(t, errResp.TableName)
	assert.Equal(t, "users", *errResp.TableName)
	require.NotNil(t, errResp.ConstraintName)
	assert.Equal(t, "users_pkey", *errResp.ConstraintName)
}

func TestConformance_ErrorResponse_SyntaxError(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELEC 1", pgfake.Result{
		PgError: &pgproto3.ErrorResponse{
			Severity: "ERROR",
			Code:     "42601",
			Message:  "syntax error at or near \"SELEC\"",
			Position: 1,
		},
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELEC 1"}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	require.NotNil(t, errResp.Code)
	assert.Equal(t, "42601", *errResp.Code)
}

// ---------------------------------------------------------------------------
// Header normalization — every Neon-* header maps to its standard equivalent.
// ---------------------------------------------------------------------------

func TestConformance_HeaderNormalization(t *testing.T) {
	tests := []struct {
		name    string
		neon    string
		std     string
		value   string
		wantStd string
	}{
		{"connection string", "Neon-Connection-String", "Connection-String", "postgres://x", "postgres://x"},
		{"array mode", "Neon-Array-Mode", "Array-Mode", "true", "true"},
		{"raw text output", "Neon-Raw-Text-Output", "Raw-Text-Output", "true", "true"},
		{"pool opt in", "Neon-Pool-Opt-In", "Pool-Opt-In", "true", "true"},
		{"batch read only", "Neon-Batch-Read-Only", "Batch-Read-Only", "true", "true"},
		{"batch isolation", "Neon-Batch-Isolation-Level", "Batch-Isolation-Level", "Serializable", "Serializable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			e.Use(NormalizeHeaders)
			var got string
			e.GET("/probe", func(c echo.Context) error {
				got = c.Request().Header.Get(tt.std)
				return c.NoContent(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set(tt.neon, tt.value)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStd, got, "%s should normalize to %s", tt.neon, tt.std)
		})
	}
}

// Standard header takes precedence over the Neon-prefixed one when both set.
func TestConformance_HeaderNormalization_StandardPrecedence(t *testing.T) {
	e := echo.New()
	e.Use(NormalizeHeaders)
	var got string
	e.GET("/probe", func(c echo.Context) error {
		got = c.Request().Header.Get("Connection-String")
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Connection-String", "standard")
	req.Header.Set("Neon-Connection-String", "neon")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, "standard", got)
}

// ---------------------------------------------------------------------------
// Request validation — driver-visible 4xx error shapes.
// ---------------------------------------------------------------------------

func TestConformance_Validation(t *testing.T) {
	pg := pgfake.New(t)
	e := newConformanceServer(t, pg)

	t.Run("missing query", func(t *testing.T) {
		rec := doSQL(e, `{}`, nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		var errResp ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
		assert.Equal(t, ErrMissingQuery.Error(), errResp.Message)
	})

	t.Run("empty batch", func(t *testing.T) {
		rec := doSQL(e, `{"queries":[]}`, nil)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"results":[]}`, rec.Body.String())
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		rec := doSQL(e, `not json`, nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing connection string", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"SELECT 1"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// ---------------------------------------------------------------------------
// JSONB column — driver expects parsed JSON, not a string.
// ---------------------------------------------------------------------------

func TestConformance_JSONBColumn(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT data FROM docs", pgfake.Result{
		Cols: []pgfake.Col{{Name: "data", TypeOID: oidJSONB}},
		Rows: [][]any{{[]byte(`{"role":"admin","n":42}`)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT data FROM docs"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	row0 := result.Rows[0].(map[string]any)
	data, ok := row0["data"].(map[string]any)
	require.True(t, ok, "jsonb should decode to an object, got %T", row0["data"])
	assert.Equal(t, "admin", data["role"])
	assert.EqualValues(t, 42, data["n"])
}

// ---------------------------------------------------------------------------
// int8 / numeric / money returned as strings to avoid JS precision loss.
// This mirrors @neondatabase/serverless, whose default parsers keep these
// types as strings (a JS Number cannot represent |value| > 2^53 exactly).
// ---------------------------------------------------------------------------

func TestConformance_Int8AsString(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT big FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "big", TypeOID: oidInt8}},
		Rows: [][]any{{int64(9223372036854775807)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT big FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Assert against the raw JSON: the value must be the quoted string
	// "9223372036854775807", not the number 9223372036854775807.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"9223372036854775807"`, string(rows[0]["big"]),
		"int8 must serialize as a JSON string for Neon-driver parity")
}

func TestConformance_NumericAsString(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT n FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidNumeric}},
		Rows: [][]any{{mustNumericConf("123456789.123456789")}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT n FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"123456789.123456789"`, string(rows[0]["n"]),
		"numeric must serialize as a JSON string to preserve precision")
}

// int8[] arrays coerce element-wise to arrays of strings (same precision
// rationale as scalar int8).
func TestConformance_Int8ArrayAsStrings(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt8Array}},
		Rows: [][]any{{[]int64{1, 9223372036854775807}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `["1","9223372036854775807"]`, string(rows[0]["a"]))
}

// A uuid column reaches the client as its canonical dashed text, the way the
// driver returns it. pgx decodes uuid to [16]byte and encoding/json renders a
// byte array as 16 numbers, so without coercion a uuid primary key arrives as
// [160,238,188,...] and no client parses that back into an id.
func TestConformance_UUIDAsString(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT id FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "id", TypeOID: oidUUID}},
		Rows: [][]any{{[16]byte{
			0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8,
			0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11,
		}}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT id FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"`, string(rows[0]["id"]),
		"uuid must serialize as its text form, not as a byte array")
}

// money[] reaches the client as an array of strings, matching the scalar money
// contract one row above. pgx registers no codec for OID 791, so the column
// arrives as the raw array literal and would otherwise serialize as a single
// JSON string that a client cannot index.
func TestConformance_MoneyArrayAsStrings(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidMoneyArray}},
		Rows: [][]any{{"{$1.23,$2.34}"}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `["$1.23","$2.34"]`, string(rows[0]["a"]))
}

// A multi-dimensional money[] keeps its shape. The literal carries its own
// braces, so the split has to track them rather than tear "{$1.23" off as an
// element.
func TestConformance_MultiDimensionalMoneyArray(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidMoneyArray}},
		Rows: [][]any{{`{{"$1,000.00",$2.34},{NULL,$4.56}}`}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `[["$1,000.00","$2.34"],[null,"$4.56"]]`, string(rows[0]["a"]))
}

// macaddr8[] reaches the client as an array of strings. pgx registers no codec
// for OID 775, so like money[] the column arrives as one array literal.
func TestConformance_Macaddr8ArrayAsStrings(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidMacaddr8Array}},
		Rows: [][]any{{"{08:00:2b:01:02:03:04:05,NULL,08:00:2b:01:02:03:04:06}"}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `["08:00:2b:01:02:03:04:05",null,"08:00:2b:01:02:03:04:06"]`, string(rows[0]["a"]))
}

// Types whose pgx Go value does not JSON-marshal to PostgreSQL's own text reach
// the client as text, not as the Go shape pgx happened to decode into. Without
// coercion bytea and xml arrive base64-encoded, date arrives as an RFC 3339
// instant, and time and interval arrive as pgtype structs that leak the field
// names Microseconds and Valid into a public API response.
func TestConformance_TextRenderedTypes(t *testing.T) {
	tests := []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{"bytea", oidBytea, []byte{0x01, 0x02}, `"\\x0102"`},
		{"xml", oidXML, []byte("<a/>"), `"<a/>"`},
		{"date", oidDate, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), `"2024-01-01"`},
		{"time", oidTime, pgtype.Time{Microseconds: 45296000000, Valid: true}, `"12:34:56"`},
		{"interval", oidInterval, pgtype.Interval{Days: 1, Valid: true}, `"1 day"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT c FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "c", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT c FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			assert.JSONEq(t, tc.want, string(rows[0]["c"]))
		})
	}
}

// A bytea[] column reaches the client as an array of text elements, matching the
// scalar bytea contract above rather than an array of base64 strings.
func TestConformance_ByteaArrayAsText(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidByteaArray}},
		Rows: [][]any{{[][]byte{{0x01, 0x02}, {0x03}}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `["\\x0102","\\x03"]`, string(rows[0]["a"]))
}

// A SQL NULL in one of the newly coerced columns stays null rather than becoming
// the empty string: coercion must not invent a value for a missing one.
func TestConformance_TextRenderedTypes_NullStaysNull(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidBytea}},
		Rows: [][]any{{nil}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT c FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `null`, string(rows[0]["c"]))
}

// ---------------------------------------------------------------------------
// Raw-text output (Neon-Raw-Text-Output) — every column value is returned as
// its raw PostgreSQL text representation (a JSON string), or null for SQL NULL.
//
// This is the @neondatabase/serverless driver's *primary* code path: it always
// sets Neon-Raw-Text-Output:true (and Neon-Array-Mode:true) because it runs its
// own node-postgres type parsers client-side keyed by dataTypeID. The server
// must therefore emit verbatim PG text, not pgx-decoded native values.
// ---------------------------------------------------------------------------

func TestConformance_RawTextOutput_ScalarsAndNull(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT n, name, flag, missing FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "n", TypeOID: oidInt4},
			{Name: "name", TypeOID: oidText},
			{Name: "flag", TypeOID: oidBool},
			{Name: "missing", TypeOID: oidText},
		},
		Rows: [][]any{{int32(42), "alice", true, nil}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT n, name, flag, missing FROM t"}`, map[string]string{
		"Neon-Raw-Text-Output": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Assert against raw JSON: every non-null value is a string, NULL is null.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)

	assert.JSONEq(t, `"42"`, string(rows[0]["n"]), "int4 must be raw text, not a JSON number")
	assert.JSONEq(t, `"alice"`, string(rows[0]["name"]))
	assert.JSONEq(t, `"t"`, string(rows[0]["flag"]), "bool must be raw PG text 't'/'f'")
	assert.JSONEq(t, `null`, string(rows[0]["missing"]), "SQL NULL must stay null")
}

// In raw-text mode, an array column comes back as its single PG array-literal
// text string (e.g. "{1,2,3}"), not a JSON array — the driver parses it
// client-side. This is the key difference from the default typed path, which
// element-wise coerces int8[] to a JSON array of strings.
func TestConformance_RawTextOutput_ArrayLiteral(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4Array}},
		Rows: [][]any{{[]int32{1, 2, 3}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a FROM t"}`, map[string]string{
		"Neon-Raw-Text-Output": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	assert.JSONEq(t, `"{1,2,3}"`, string(rows[0]["a"]),
		"array must be the raw PG array literal string in raw-text mode")
}

// Raw-text output is honored per-result inside a batch too, and composes with
// array mode (rows become positional arrays of text strings).
func TestConformance_RawTextOutput_BatchArrayMode(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 10 AS a, 'x' AS b", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "a", TypeOID: oidInt4},
			{Name: "b", TypeOID: oidText},
		},
		Rows: [][]any{{int32(10), "x"}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 10 AS a, 'x' AS b"}]}`, map[string]string{
		"Neon-Raw-Text-Output": "true",
		"Neon-Array-Mode":      "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 1)
	assert.True(t, batch.Results[0].RowAsArray)

	row0, ok := batch.Results[0].Rows[0].([]any)
	require.True(t, ok, "array mode rows must be positional arrays")
	assert.Equal(t, "10", row0[0], "raw-text values are strings even in array mode")
	assert.Equal(t, "x", row0[1])
}

// ---------------------------------------------------------------------------
// Batch transaction options — readOnly / isolationLevel / deferrable.
//
// The driver sends these as Neon-Batch-Read-Only / Neon-Batch-Isolation-Level /
// Neon-Batch-Deferrable headers on array (sql.transaction) requests. The
// handler wraps the batch in an explicit BEGIN … COMMIT so PostgreSQL applies the
// characteristics; READ ONLY makes the server reject writes with SQLSTATE 25006.
// ---------------------------------------------------------------------------

// A read-only batch rejects a write with the 25006 read-only error, surfaced as
// the driver-visible pg-error JSON. Enforcement is server-side (PostgreSQL),
// layered on top of any proxy read-only policy.
//
// Run for both output modes: native values, and raw text, which the real driver
// always sets. Both must reject the write identically.
func TestConformance_BatchReadOnly_RejectsWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"extended protocol", map[string]string{"Neon-Batch-Read-Only": "true"}},
		{"raw text", map[string]string{
			"Neon-Batch-Read-Only": "true",
			"Neon-Raw-Text-Output": "true",
			"Neon-Array-Mode":      "true",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT count(*) AS c FROM orders", pgfake.Result{
				Cols: []pgfake.Col{{Name: "c", TypeOID: oidInt4}},
				Rows: [][]any{{int32(3)}},
				Tag:  "SELECT 1",
			})
			pg.SetResult("INSERT INTO orders (id) VALUES (1)", pgfake.Result{
				IsWrite: true, // PostgreSQL would reject this inside READ ONLY
				Tag:     "INSERT 0 1",
			})
			e := newConformanceServer(t, pg)

			body := `{"queries":[` +
				`{"query":"SELECT count(*) AS c FROM orders"},` +
				`{"query":"INSERT INTO orders (id) VALUES (1)"}]}`
			rec := doSQL(e, body, tc.headers)

			// 25006 (read_only_sql_transaction) is class 25 -> default 400 mapping.
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			var errResp ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
			require.NotNil(t, errResp.Code)
			assert.Equal(t, "25006", *errResp.Code)
			assert.Contains(t, errResp.Message, "read-only transaction")

			// The transaction was opened READ ONLY.
			assert.Contains(t, pg.LastBegin(), "READ ONLY")
		})
	}
}

// A read-only batch of pure reads succeeds and returns per-statement results.
func TestConformance_BatchReadOnly_AllowsReads(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT count(*) AS c FROM orders", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidInt4}},
		Rows: [][]any{{int32(7)}},
		Tag:  "SELECT 1",
	})
	pg.SetResult("SELECT sum(amount) AS s FROM orders", pgfake.Result{
		Cols: []pgfake.Col{{Name: "s", TypeOID: oidInt4}},
		Rows: [][]any{{int32(99)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	body := `{"queries":[` +
		`{"query":"SELECT count(*) AS c FROM orders"},` +
		`{"query":"SELECT sum(amount) AS s FROM orders"}]}`
	rec := doSQL(e, body, map[string]string{
		"Neon-Batch-Read-Only":       "true",
		"Neon-Batch-Isolation-Level": "RepeatableRead",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 2)
	assert.EqualValues(t, 7, batch.Results[0].Rows[0].(map[string]any)["c"])
	assert.EqualValues(t, 99, batch.Results[1].Rows[0].(map[string]any)["s"])

	// Isolation level + read-only both applied to the BEGIN.
	begin := pg.LastBegin()
	assert.Contains(t, begin, "ISOLATION LEVEL REPEATABLE READ")
	assert.Contains(t, begin, "READ ONLY")
}

// isolationLevel alone (no readOnly) is applied to the BEGIN, and the batch runs
// normally. Covers the Serializable + deferrable combination too.
func TestConformance_BatchIsolationLevel_Serializable(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS one", pgfake.Result{
		Cols: []pgfake.Col{{Name: "one", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1 AS one"}]}`, map[string]string{
		"Neon-Batch-Isolation-Level": "Serializable",
		"Neon-Batch-Read-Only":       "true",
		"Neon-Batch-Deferrable":      "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	begin := pg.LastBegin()
	assert.Contains(t, begin, "ISOLATION LEVEL SERIALIZABLE")
	assert.Contains(t, begin, "READ ONLY")
	assert.Contains(t, begin, "DEFERRABLE")
}

// An unrecognized isolation level fails closed with a 400 rather than silently
// dropping the guarantee the caller asked for.
func TestConformance_BatchIsolationLevel_Invalid(t *testing.T) {
	pg := pgfake.New(t)
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1"}]}`, map[string]string{
		"Neon-Batch-Isolation-Level": "Bogus",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Message, "invalid isolation level")
}

// A read-only request spelled "True" is still a read-only request. Header
// values are not case-normalized anywhere in the stack, so a client that does
// not spell it exactly like the driver does must not silently get a read-write
// batch: the write is still rejected with 25006 and the BEGIN still says READ
// ONLY.
func TestConformance_BatchReadOnly_CapitalizedValue(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("INSERT INTO orders (id) VALUES (1)", pgfake.Result{
		IsWrite: true, // PostgreSQL would reject this inside READ ONLY
		Tag:     "INSERT 0 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"INSERT INTO orders (id) VALUES (1)"}]}`,
		map[string]string{"Neon-Batch-Read-Only": "True"})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	require.NotNil(t, errResp.Code)
	assert.Equal(t, "25006", *errResp.Code)
	assert.Contains(t, pg.LastBegin(), "READ ONLY")
}

// A read-only value we cannot read fails closed with a 400, the same way an
// unrecognized isolation level does, rather than running the batch read-write.
func TestConformance_BatchReadOnly_UnreadableValue(t *testing.T) {
	pg := pgfake.New(t)
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1"}]}`,
		map[string]string{"Neon-Batch-Read-Only": "yes"})

	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Message, "Batch-Read-Only")
}

// A raw-text batch with no transaction options takes the plain
// implicit-transaction pipeline (no explicit BEGIN), the driver's fast path.
func TestConformance_BatchNoOptions_NoExplicitBegin(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS one", pgfake.Result{
		Cols: []pgfake.Col{{Name: "one", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1 AS one"}]}`, map[string]string{"Neon-Raw-Text-Output": "true"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, pg.LastBegin(), "no transaction options => no explicit BEGIN")
}

// A committed batch echoes the characteristics it ran with, as Neon's proxy does;
// a single query, a failed batch and an unset or false option carry none.
func TestConformance_BatchEchoesTransactionHeaders(t *testing.T) {
	echoed := []string{"Neon-Batch-Read-Only", "Neon-Batch-Deferrable", "Neon-Batch-Isolation-Level"}
	for _, tc := range []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		want    map[string]string
	}{
		{
			name: "all three",
			body: `{"queries":[{"query":"SELECT 1 AS one"}]}`,
			headers: map[string]string{
				"Neon-Batch-Isolation-Level": "Serializable",
				"Neon-Batch-Read-Only":       "true",
				"Neon-Batch-Deferrable":      "true",
			},
			status: http.StatusOK,
			want: map[string]string{
				"Neon-Batch-Isolation-Level": "Serializable",
				"Neon-Batch-Read-Only":       "true",
				"Neon-Batch-Deferrable":      "true",
			},
		},
		{
			name:    "isolation level in another spelling",
			body:    `{"queries":[{"query":"SELECT 1 AS one"}]}`,
			headers: map[string]string{"Batch-Isolation-Level": "repeatable read"},
			status:  http.StatusOK,
			want:    map[string]string{"Neon-Batch-Isolation-Level": "RepeatableRead"},
		},
		{
			name:    "read only spelled True",
			body:    `{"queries":[{"query":"SELECT 1 AS one"}]}`,
			headers: map[string]string{"Neon-Batch-Read-Only": "True"},
			status:  http.StatusOK,
			want:    map[string]string{"Neon-Batch-Read-Only": "true"},
		},
		{
			name: "false options",
			body: `{"queries":[{"query":"SELECT 1 AS one"}]}`,
			headers: map[string]string{
				"Neon-Batch-Read-Only":  "false",
				"Neon-Batch-Deferrable": "false",
			},
			status: http.StatusOK,
		},
		{
			name:   "no options",
			body:   `{"queries":[{"query":"SELECT 1 AS one"}]}`,
			status: http.StatusOK,
		},
		{
			name:    "single query",
			body:    `{"query":"SELECT 1 AS one"}`,
			headers: map[string]string{"Neon-Batch-Read-Only": "true"},
			status:  http.StatusOK,
		},
		{
			name: "raw text",
			body: `{"queries":[{"query":"SELECT 1 AS one"}]}`,
			headers: map[string]string{
				"Neon-Batch-Read-Only": "true",
				"Neon-Raw-Text-Output": "true",
				"Neon-Array-Mode":      "true",
			},
			status: http.StatusOK,
			want:   map[string]string{"Neon-Batch-Read-Only": "true"},
		},
		{
			name:    "rejected write",
			body:    `{"queries":[{"query":"INSERT INTO orders (id) VALUES (1)"}]}`,
			headers: map[string]string{"Neon-Batch-Read-Only": "true"},
			status:  http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1 AS one", pgfake.Result{
				Cols: []pgfake.Col{{Name: "one", TypeOID: oidInt4}},
				Rows: [][]any{{int32(1)}},
				Tag:  "SELECT 1",
			})
			pg.SetResult("INSERT INTO orders (id) VALUES (1)", pgfake.Result{IsWrite: true, Tag: "INSERT 0 1"})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, tc.body, tc.headers)

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			for _, name := range echoed {
				assert.Equal(t, tc.want[name], rec.Header().Get(name), name)
			}
		})
	}
}
