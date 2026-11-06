package pgserverless

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overCap is smaller than the fields of any result here, so the first row is refused.
const overCap = 100

// fitCap is larger than any response here.
const fitCap = 10_000

// tenTextRows is ten rows of a ten-byte text value.
func tenTextRows() pgfake.Result {
	rows := make([][]any, 10)
	for i := range rows {
		rows[i] = []any{strings.Repeat("x", 10)}
	}
	return pgfake.Result{Cols: []pgfake.Col{{Name: "v", TypeOID: oidText}}, Rows: rows, Tag: "SELECT 10"}
}

func cappedServers(pg *pgfake.PG, maxBytes int64) map[string]http.Handler {
	return admittedCapServers(pg, maxBytes, 0)
}

// admittedCapServers caps the handler at handlerCap and has its Admission set
// Upstream.MaxResponseBytes to requestCap.
func admittedCapServers(pg *pgfake.PG, handlerCap, requestCap int64) map[string]http.Handler {
	static := StaticUpstream(pg.Addr())
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		up.MaxResponseBytes = requestCap
		return up, err
	}, WithMaxResponseBytes(handlerCap)).Register(e)

	staticHTTP := StaticHTTPUpstream(pg.Addr())
	std := NewHTTPHandler(func(r *http.Request) (Upstream, error) {
		up, err := staticHTTP(r)
		up.InsecureSkipVerify = true
		up.MaxResponseBytes = requestCap
		return up, err
	}, WithHTTPMaxResponseBytes(handlerCap))

	return map[string]http.Handler{"echo": e, "net/http": std}
}

func TestResponseOverTheCapIs507(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())

	for name, h := range cappedServers(pg, overCap) {
		for mode, headers := range map[string]map[string]string{
			"native":   nil,
			"raw text": {"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 100 bytes)", decodeError(t, rec).Message)
			})
		}
	}
}

// A statement whose result is still streaming past the cap must answer 507 at
// once; draining the rest held the response until the request ended, which then
// answered 400 Query cancelled.
func TestSingleStatementOverTheCapStopsReading(t *testing.T) {
	pg := pgfake.New(t)
	streaming := tenTextRows()
	streaming.HoldAfterRows = true
	pg.SetResult("SELECT v FROM t", streaming)

	for name, h := range cappedServers(pg, overCap) {
		for mode, headers := range map[string]map[string]string{
			"native":   nil,
			"raw text": driverHeaders,
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQLUntil(t, h, `{"query":"SELECT v FROM t"}`, headers, 2*time.Second)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 100 bytes)", decodeError(t, rec).Message)
			})
		}
	}
}

// uncappedBody is the response to body from an uncapped server, without the
// newline the encoder ends it with.
func uncappedBody(t *testing.T, pg *pgfake.PG, body string, headers map[string]string) string {
	t.Helper()
	rec := doSQL(cappedServers(pg, 0)["net/http"], body, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return strings.TrimSuffix(rec.Body.String(), "\n")
}

// Neon refuses only when a row arrives after its buffer passed the cap, so the
// row that crosses it is still sent: a result fits once everything before its
// last row does.
func TestRowThatCrossesTheCapIsSent(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())
	const lastRow = `,{"v":"xxxxxxxxxx"}`
	const trailer = `],"command":"SELECT","rowCount":10,"rowAsArray":false}`
	body := uncappedBody(t, pg, `{"query":"SELECT v FROM t"}`, nil)
	require.Contains(t, body, `{"v":"xxxxxxxxxx"}]`)
	beforeLastRow := int64(len(body) - len(lastRow) - len(trailer))

	for name, h := range cappedServers(pg, beforeLastRow) {
		t.Run(name+"/fits", func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
	for name, h := range cappedServers(pg, beforeLastRow-1) {
		t.Run(name+"/one byte under", func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

			require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
		})
	}
}

// The driver's raw-text rows are smaller as JSON than as DataRows: ["1"] is 5
// bytes where its DataRow is 12, so counting DataRows refused a result Neon sends.
func TestCapCountsRawTextRowsAsJSON(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1 FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "?column?", TypeOID: oidText}},
		Rows: repeatRow([]any{"1"}, 120), Tag: "SELECT 120",
	})

	for name, h := range cappedServers(pg, 1000) {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT 1 FROM t"}`, driverHeaders)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

// An object-mode row repeats every column name, so its JSON is larger than its
// DataRow, and counting DataRows let a response through at more than its cap.
func TestCapCountsObjectRowsAsJSON(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT x FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a_long_column_name", TypeOID: oidText}},
		Rows: repeatRow([]any{"x"}, 50), Tag: "SELECT 50",
	})

	for name, h := range cappedServers(pg, 1000) {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT x FROM t"}`, nil)

			require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
		})
	}
}

// Rows whose values are all NULL, or that have no columns, still cost their
// JSON; counting values alone let an endless result of them through.
func TestRowsWithoutValueBytesCountAgainstTheCap(t *testing.T) {
	for name, result := range map[string]pgfake.Result{
		"null values": {Cols: []pgfake.Col{{Name: "v", TypeOID: oidText}}, Rows: repeatRow([]any{nil}, 100), Tag: "SELECT 100"},
		"no columns":  {Rows: repeatRow([]any{}, 100), Tag: "SELECT 100"},
	} {
		pg := pgfake.New(t)
		pg.SetResult("SELECT v FROM t", result)
		for server, h := range cappedServers(pg, 200) {
			t.Run(name+"/"+server, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 200 bytes)", decodeError(t, rec).Message)
			})
		}
	}
}

func repeatRow(row []any, n int) [][]any {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = row
	}
	return rows
}

func TestBatchStatementsShareTheResponseCap(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())
	pg.SetResult("SELECT v FROM u", tenTextRows())
	batch := `{"queries":[{"query":"SELECT v FROM t"},{"query":"SELECT v FROM u"}]}`
	// The first result fits whole inside the batch envelope; the second cannot.
	oneResult := int64(len(`{"results":[`) + len(uncappedBody(t, pg, `{"query":"SELECT v FROM t"}`, nil)))
	want := fmt.Sprintf("response is too large (max is %d bytes)", oneResult)

	for name, h := range cappedServers(pg, oneResult) {
		for mode, headers := range map[string]map[string]string{
			"implicit":  nil,
			"read only": {"Neon-Batch-Read-Only": "true"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQL(h, batch, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, want, decodeError(t, rec).Message)
			})
		}
	}
}

const writeAfterSelect = `{"queries":[{"query":"SELECT v FROM t"},{"query":"INSERT INTO log VALUES (1)"}]}`

// capServer builds one capped server on its own fake, so each case reads only
// the writes it made.
type capServer func(t *testing.T, maxBytes int64) (*pgfake.PG, http.Handler)

func capServers() map[string]capServer {
	servers := map[string]capServer{}
	for _, name := range []string{"echo", "net/http"} {
		servers[name] = func(t *testing.T, maxBytes int64) (*pgfake.PG, http.Handler) {
			pg := writeAfterSelectPG(t)
			return pg, cappedServers(pg, maxBytes)[name]
		}
	}
	servers["governed simple protocol"] = func(t *testing.T, maxBytes int64) (*pgfake.PG, http.Handler) {
		pg := writeAfterSelectPG(t)
		e := echo.New()
		static := StaticUpstream(pg.Addr())
		NewHandler(func(c echo.Context) (Upstream, error) {
			up, err := static(c)
			up.InsecureSkipVerify = true
			up.RawTextSimpleProtocol = true
			return up, err
		}, WithStatementCounter(semicolonCount), WithMaxResponseBytes(maxBytes)).Register(e)
		return pg, e
	}
	return servers
}

func writeAfterSelectPG(t *testing.T) *pgfake.PG {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())
	pg.SetResult("INSERT INTO log VALUES (1)", pgfake.Result{Tag: "INSERT 0 1", IsWrite: true})
	return pg
}

var capBatchModes = map[string]map[string]string{
	"implicit":    driverHeaders,
	"transaction": {"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true", "Neon-Batch-Isolation-Level": "Serializable"},
}

// PostgreSQL can run the whole batch before the handler reads past the cap, so
// a 507 must roll back a write queued after the oversized result, as Neon does.
func TestBatchOverTheCapDoesNotPersistLaterWrites(t *testing.T) {
	for name, server := range capServers() {
		for mode, headers := range capBatchModes {
			t.Run(name+"/"+mode, func(t *testing.T) {
				pg, h := server(t, overCap)

				rec := doSQL(h, writeAfterSelect, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Empty(t, pg.CommittedWrites())
			})
		}
	}
}

func TestBatchUnderTheCapCommitsItsWrites(t *testing.T) {
	for name, server := range capServers() {
		for mode, headers := range capBatchModes {
			t.Run(name+"/"+mode, func(t *testing.T) {
				pg, h := server(t, fitCap)

				rec := doSQL(h, writeAfterSelect, headers)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.Equal(t, []string{"INSERT INTO log VALUES (1)"}, pg.CommittedWrites())
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

func TestAdmissionOverridesTheResponseCap(t *testing.T) {
	for name, tc := range map[string]struct {
		handlerCap, requestCap int64
		status                 int
	}{
		"lowers the handler's":     {fitCap, overCap, http.StatusInsufficientStorage},
		"raises the handler's":     {overCap, fitCap, http.StatusOK},
		"caps an uncapped one":     {0, overCap, http.StatusInsufficientStorage},
		"negative lifts the cap":   {overCap, -1, http.StatusOK},
		"zero keeps the handler's": {overCap, 0, http.StatusInsufficientStorage},
	} {
		pg := pgfake.New(t)
		pg.SetResult("SELECT v FROM t", tenTextRows())
		for server, h := range admittedCapServers(pg, tc.handlerCap, tc.requestCap) {
			t.Run(name+"/"+server, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

				require.Equal(t, tc.status, rec.Code, rec.Body.String())
				if tc.status == http.StatusInsufficientStorage {
					assert.Equal(t, "response is too large (max is 100 bytes)", decodeError(t, rec).Message)
				}
			})
		}
	}
}

// A capped governed batch runs inside the module's own BEGIN so a 507 rolls it
// back; with the cap lifted for the request it runs as one implicit block again.
func TestLiftedCapGovernedBatchOpensNoTransaction(t *testing.T) {
	pg := writeAfterSelectPG(t)
	e := echo.New()
	static := StaticUpstream(pg.Addr())
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		up.RawTextSimpleProtocol = true
		up.MaxResponseBytes = -1
		return up, err
	}, WithStatementCounter(semicolonCount), WithMaxResponseBytes(overCap)).Register(e)

	rec := doSQL(e, writeAfterSelect, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, pg.LastBegin())
	assert.Equal(t, []string{"INSERT INTO log VALUES (1)"}, pg.CommittedWrites())
}

// The budget grows as Neon's output buffer does, byte for byte.
func TestResponseBudgetFollowsNeonsBuffer(t *testing.T) {
	fields := []FieldDefinition{{Name: "v", DataTypeID: oidText, DataTypeSize: -1, DataTypeModifier: -1, Format: "text"}}
	const field = `{"name":"v","dataTypeID":25,"tableID":0,"columnID":0,"dataTypeSize":-1,"dataTypeModifier":-1,"format":"text"}`
	result := func(rows string, n int) string {
		return `{"fields":[` + field + `],"rows":[` + rows + fmt.Sprintf(`],"command":"SELECT","rowCount":%d,"rowAsArray":true}`, n)
	}
	charge := func(b *responseBudget, rows ...any) {
		b.startResult(fields)
		for _, r := range rows {
			require.NoError(t, b.addRow(r))
		}
		n := len(rows)
		b.endResult(&QueryResult{Fields: fields, Command: "SELECT", RowCount: &n, RowAsArray: true})
	}

	single := &responseBudget{max: fitCap}
	charge(single, []any{"a<b"}, []any{nil})
	assert.Equal(t, int64(len(result(`["a<b"],[null]`, 2))), single.used)

	object := &responseBudget{max: fitCap}
	object.startResult(fields)
	require.NoError(t, object.addRow(ObjectRow{Names: []string{"v"}, Values: []any{"a<b&c"}}))
	one := 1
	object.endResult(&QueryResult{Fields: fields, Command: "SELECT", RowCount: &one})
	assert.Equal(t, int64(len(`{"fields":[`+field+`],"rows":[{"v":"a<b&c"}],"command":"SELECT","rowCount":1,"rowAsArray":false}`)), object.used)

	batch := &responseBudget{max: fitCap, batch: true}
	charge(batch, []any{"a"})
	charge(batch)
	empty := emptyResult(true)
	batch.chargeResult(&empty)
	emptyJSON := `{"fields":[],"rows":[],"command":"","rowCount":null,"rowAsArray":true}`
	assert.Equal(t, int64(len(`{"results":[`+result(`["a"]`, 1)+`,`+result(``, 0)+`,`+emptyJSON)), batch.used)
}
