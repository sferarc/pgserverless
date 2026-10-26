package pgserverless

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// The headers @neondatabase/serverless sends on every HTTP query.
var driverHeaders = map[string]string{
	"Neon-Raw-Text-Output": "true",
	"Neon-Array-Mode":      "true",
}

func TestConformance_RawTextOutput_BindsUntypedTextParams(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT $1::int4 AS v", pgfake.Result{
		Cols: []pgfake.Col{{Name: "v", TypeOID: oidInt4}},
		Rows: [][]any{{int32(42)}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT $1::int4 AS v","params":[42]}`, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.Len(t, res.Rows, 1)
	assert.Equal(t, []any{"42"}, res.Rows[0])
	assert.Empty(t, pg.LastSimpleSQL(), "raw text must not interpolate params into a simple query")
	assert.Equal(t, [][]byte{[]byte("42")}, pg.LastBindParams())
}

// Neon prepares every query, so PostgreSQL refuses a query holding two
// statements instead of running both and returning the first result.
func TestConformance_MultiStatementQueryRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"raw text", driverHeaders},
		{"native", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1 AS a", pgfake.Result{
				Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
				Rows: [][]any{{int32(1)}},
			})
			pg.SetResult("DELETE FROM orders", pgfake.Result{Tag: "DELETE 3", IsWrite: true})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT 1 AS a; DELETE FROM orders"}`, tc.headers)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			resp := decodeError(t, rec)
			require.NotNil(t, resp.Code)
			assert.Equal(t, "42601", *resp.Code)
			assert.Equal(t, "cannot insert multiple commands into a prepared statement", resp.Message)
		})
	}
}

// Each batch item is its own statement, so one holding two is refused rather
// than shifting every later item onto the wrong result.
func TestConformance_RawTextOutput_BatchItemWithTwoStatementsRejected(t *testing.T) {
	pg := pgfake.New(t)
	for i, q := range []string{"SELECT 1 AS a", "SELECT 2 AS b", "SELECT 3 AS c"} {
		pg.SetResult(q, pgfake.Result{
			Cols: []pgfake.Col{{Name: "v", TypeOID: oidInt4}},
			Rows: [][]any{{int32(i + 1)}},
		})
	}
	e := newConformanceServer(t, pg)

	body := `{"queries":[{"query":"SELECT 1 AS a; SELECT 2 AS b"},{"query":"SELECT 3 AS c"}]}`
	rec := doSQL(e, body, driverHeaders)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "42601", *resp.Code)
}

// A raw-text batch used to be joined with ';' into one simple query, where an
// item ending in a line comment commented out the item after it.
func TestConformance_RawTextOutput_BatchItemEndingInLineComment(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a -- first", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
	})
	pg.SetResult("SELECT 2 AS b", pgfake.Result{
		Cols: []pgfake.Col{{Name: "b", TypeOID: oidInt4}},
		Rows: [][]any{{int32(2)}},
	})
	e := newConformanceServer(t, pg)

	body := `{"queries":[{"query":"SELECT 1 AS a -- first"},{"query":"SELECT 2 AS b"}]}`
	rec := doSQL(e, body, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 2)
	assert.Equal(t, []any{"1"}, batch.Results[0].Rows[0])
	assert.Equal(t, []any{"2"}, batch.Results[1].Rows[0])
	// pgfake does not model comments, so the protocol is what pins this.
	assert.Empty(t, pg.LastSimpleSQL(), "a raw-text batch must not be joined into one simple query")
}

// newGovernedConformanceServer serves /sql the way the proxy does for a session
// it may govern by policy: raw-text requests stay on the simple protocol, with
// count standing in for the proxy's PostgreSQL parser.
func newGovernedConformanceServer(pg *pgfake.PG, count func(string) (int, error)) *echo.Echo {
	e := echo.New()
	static := StaticUpstream(pg.Addr())
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		up.RawTextSimpleProtocol = true
		return up, err
	}, WithStatementCounter(count)).Register(e)
	return e
}

// semicolonCount counts the non-blank parts between semicolons, which is what
// PostgreSQL counts for SQL with no semicolon inside a literal or comment.
func semicolonCount(sql string) (int, error) {
	n := 0
	for part := range strings.SplitSeq(sql, ";") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n, nil
}

// Without a statement counter the simple protocol could run a hidden second
// statement, so the handler keeps Neon's extended protocol instead.
func TestRawTextSimpleProtocol_NeedsStatementCounter(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
	})
	e := newGovernedConformanceServer(pg, nil)

	rec := doSQL(e, `{"query":"SELECT 1 AS a"}`, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, pg.SimpleSQL())
}

func TestRawTextSimpleProtocol_KeepsSimpleQueries(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
	})
	e := newGovernedConformanceServer(pg, semicolonCount)

	rec := doSQL(e, `{"query":"SELECT 1 AS a"}`, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "SELECT 1 AS a", pg.LastSimpleSQL())
}

// A simple query would run every statement it holds, so a governed session
// refuses one with two as Neon's prepare does, before anything reaches upstream.
func TestConformance_Governed_MultiStatementQueryRejected(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
	})
	pg.SetResult("DELETE FROM orders", pgfake.Result{Tag: "DELETE 3", IsWrite: true})
	e := newGovernedConformanceServer(pg, semicolonCount)

	rec := doSQL(e, `{"query":"SELECT 1 AS a; DELETE FROM orders"}`, driverHeaders)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "42601", *resp.Code)
	assert.Equal(t, "cannot insert multiple commands into a prepared statement", resp.Message)
	assert.Empty(t, pg.SimpleSQL())
}

// A governed batch is still one joined simple query, with a newline on each
// side of every ';' so an item ending in a line comment cannot swallow the next.
func TestConformance_Governed_BatchItemEndingInLineComment(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a -- first", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
	})
	pg.SetResult("SELECT 2 AS b", pgfake.Result{
		Cols: []pgfake.Col{{Name: "b", TypeOID: oidInt4}},
		Rows: [][]any{{int32(2)}},
	})
	e := newGovernedConformanceServer(pg, semicolonCount)

	body := `{"queries":[{"query":"SELECT 1 AS a -- first"},{"query":"SELECT 2 AS b"}]}`
	rec := doSQL(e, body, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var batch BatchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &batch))
	require.Len(t, batch.Results, 2)
	assert.Equal(t, []any{"1"}, batch.Results[0].Rows[0])
	assert.Equal(t, []any{"2"}, batch.Results[1].Rows[0])
	assert.Equal(t, []string{"SELECT 1 AS a -- first\n;\nSELECT 2 AS b"}, pg.SimpleSQL())
}

// A governed batch item holding two statements is refused before anything is
// sent, since the joined query would shift every later result.
func TestConformance_Governed_BatchItemWithTwoStatementsRejected(t *testing.T) {
	pg := pgfake.New(t)
	for i, q := range []string{"SELECT 1 AS a", "SELECT 2 AS b", "SELECT 3 AS c"} {
		pg.SetResult(q, pgfake.Result{
			Cols: []pgfake.Col{{Name: "v", TypeOID: oidInt4}},
			Rows: [][]any{{int32(i + 1)}},
		})
	}
	e := newGovernedConformanceServer(pg, semicolonCount)

	body := `{"queries":[{"query":"SELECT 3 AS c"},{"query":"SELECT 1 AS a; SELECT 2 AS b"}]}`
	rec := doSQL(e, body, driverHeaders)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "42601", *resp.Code)
	assert.Equal(t, "cannot insert multiple commands into a prepared statement", resp.Message)
	assert.Empty(t, pg.SimpleSQL())
}

// An item the parser refuses or finds empty is refused before anything is
// sent: an unterminated comment or quote would run on into the next item, and
// an empty one would shift every later result.
func TestConformance_Governed_IncompleteBatchItemRejected(t *testing.T) {
	for _, tc := range []struct {
		name, item, message string
		n                   int
		err                 error
	}{
		{"unparseable", "SELECT 1 /* open", "unterminated /* comment", 0, errors.New("unterminated /* comment")},
		{"empty", "-- nothing", "empty query in batch", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			e := newGovernedConformanceServer(pg, func(sql string) (int, error) {
				if sql == tc.item {
					return tc.n, tc.err
				}
				return semicolonCount(sql)
			})

			items, err := json.Marshal([]QueryItem{{Query: tc.item}, {Query: "SELECT 2 AS b"}})
			require.NoError(t, err)
			rec := doSQL(e, `{"queries":`+string(items)+`}`, driverHeaders)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			resp := decodeError(t, rec)
			require.NotNil(t, resp.Code)
			assert.Equal(t, "42601", *resp.Code)
			assert.Equal(t, tc.message, resp.Message)
			assert.Empty(t, pg.SimpleSQL())
		})
	}
}

// Batch characteristics still bracket the joined query in BEGIN and COMMIT,
// with the separator newlines kept next to both.
func TestConformance_Governed_BatchReadOnlyRejectsWrite(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("INSERT INTO orders (id) VALUES (1)", pgfake.Result{IsWrite: true, Tag: "INSERT 0 1"})
	e := newGovernedConformanceServer(pg, semicolonCount)

	headers := map[string]string{"Neon-Batch-Read-Only": "true"}
	maps.Copy(headers, driverHeaders)
	rec := doSQL(e, `{"queries":[{"query":"INSERT INTO orders (id) VALUES (1)"}]}`, headers)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	resp := decodeError(t, rec)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "25006", *resp.Code)
	assert.Equal(t, []string{"BEGIN READ ONLY;\nINSERT INTO orders (id) VALUES (1)\n;COMMIT"}, pg.SimpleSQL())
}

// A one-item governed batch is sent exactly as it was before the separator
// newlines, so the proxy sees the same text.
func TestConformance_Governed_SingleItemBatchSentVerbatim(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 AS a", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
	})
	e := newGovernedConformanceServer(pg, semicolonCount)

	rec := doSQL(e, `{"queries":[{"query":"SELECT 1 AS a"}]}`, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"SELECT 1 AS a"}, pg.SimpleSQL())
}
