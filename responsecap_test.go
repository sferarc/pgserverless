package pgserverless

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tenTextRowsBytes is what tenTextRows charges the cap: each DataRow is 7 bytes
// of framing, a 4 byte length word and the 10 byte value.
const tenTextRowsBytes = 10 * (7 + 4 + 10)

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

	for name, h := range cappedServers(pg, tenTextRowsBytes-1) {
		for mode, headers := range map[string]map[string]string{
			"native":   nil,
			"raw text": {"Neon-Raw-Text-Output": "true", "Neon-Array-Mode": "true"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 209 bytes)", decodeError(t, rec).Message)
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

	for name, h := range cappedServers(pg, tenTextRowsBytes-1) {
		for mode, headers := range map[string]map[string]string{
			"native":   nil,
			"raw text": driverHeaders,
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQLUntil(t, h, `{"query":"SELECT v FROM t"}`, headers, 2*time.Second)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 209 bytes)", decodeError(t, rec).Message)
			})
		}
	}
}

func TestResponseAtTheCapRuns(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT v FROM t", tenTextRows())

	for name, h := range cappedServers(pg, tenTextRowsBytes) {
		t.Run(name, func(t *testing.T) {
			rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

// Rows whose values are all NULL, or that have no columns, still cost their
// framing; counting values alone let an endless result of them through.
func TestRowsWithoutValueBytesCountAgainstTheCap(t *testing.T) {
	for name, result := range map[string]pgfake.Result{
		"null values": {Cols: []pgfake.Col{{Name: "v", TypeOID: oidText}}, Rows: repeatRow([]any{nil}, 100), Tag: "SELECT 100"},
		"no columns":  {Rows: repeatRow([]any{}, 100), Tag: "SELECT 100"},
	} {
		pg := pgfake.New(t)
		pg.SetResult("SELECT v FROM t", result)
		for server, h := range cappedServers(pg, 500) {
			t.Run(name+"/"+server, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 500 bytes)", decodeError(t, rec).Message)
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

	for name, h := range cappedServers(pg, tenTextRowsBytes*3/2) {
		for mode, headers := range map[string]map[string]string{
			"implicit":  nil,
			"read only": {"Neon-Batch-Read-Only": "true"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				rec := doSQL(h, batch, headers)

				require.Equal(t, http.StatusInsufficientStorage, rec.Code, rec.Body.String())
				assert.Equal(t, "response is too large (max is 315 bytes)", decodeError(t, rec).Message)
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
				pg, h := server(t, tenTextRowsBytes-1)

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
				pg, h := server(t, tenTextRowsBytes)

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
		"lowers the handler's":     {tenTextRowsBytes, tenTextRowsBytes - 1, http.StatusInsufficientStorage},
		"raises the handler's":     {tenTextRowsBytes - 1, tenTextRowsBytes, http.StatusOK},
		"caps an uncapped one":     {0, tenTextRowsBytes - 1, http.StatusInsufficientStorage},
		"negative lifts the cap":   {tenTextRowsBytes - 1, -1, http.StatusOK},
		"zero keeps the handler's": {tenTextRowsBytes - 1, 0, http.StatusInsufficientStorage},
	} {
		pg := pgfake.New(t)
		pg.SetResult("SELECT v FROM t", tenTextRows())
		for server, h := range admittedCapServers(pg, tc.handlerCap, tc.requestCap) {
			t.Run(name+"/"+server, func(t *testing.T) {
				rec := doSQL(h, `{"query":"SELECT v FROM t"}`, nil)

				require.Equal(t, tc.status, rec.Code, rec.Body.String())
				if tc.status == http.StatusInsufficientStorage {
					assert.Equal(t, "response is too large (max is 209 bytes)", decodeError(t, rec).Message)
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
	}, WithStatementCounter(semicolonCount), WithMaxResponseBytes(tenTextRowsBytes-1)).Register(e)

	rec := doSQL(e, writeAfterSelect, driverHeaders)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, pg.LastBegin())
	assert.Equal(t, []string{"INSERT INTO log VALUES (1)"}, pg.CommittedWrites())
}
