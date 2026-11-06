package pgserverless

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// timestamptz_out prints in the session TimeZone, so Neon and raw-text mode say
// +05:30 for a session in Asia/Kolkata where native mode always said +00.
func TestConformance_TimestamptzInSessionTimeZone(t *testing.T) {
	instant := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	later := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	rangeOf := func(lo, hi time.Time) pgtype.Range[pgtype.Timestamptz] {
		return pgtype.Range[pgtype.Timestamptz]{
			Lower: pgtype.Timestamptz{Time: lo, Valid: true}, Upper: pgtype.Timestamptz{Time: hi, Valid: true},
			LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true,
		}
	}
	cols := []pgfake.Col{
		{Name: "ts", TypeOID: oidTimestamptz},
		{Name: "arr", TypeOID: oidTimestamptzArray},
		{Name: "r", TypeOID: pgtype.TstzrangeOID},
		{Name: "mr", TypeOID: pgtype.TstzmultirangeOID},
	}
	row := []any{
		instant,
		[]time.Time{instant, later},
		rangeOf(instant, later),
		pgtype.Multirange[pgtype.Range[pgtype.Timestamptz]]{rangeOf(instant, later)},
	}

	for _, tc := range []struct {
		zone string
		want map[string]string
	}{
		{"Asia/Kolkata", map[string]string{
			"ts":  `"2024-01-01 15:30:00+05:30"`,
			"arr": `["2024-01-01 15:30:00+05:30","2024-01-02 15:30:00+05:30"]`,
			"r":   `"[\"2024-01-01 15:30:00+05:30\",\"2024-01-02 15:30:00+05:30\")"`,
			"mr":  `"{[\"2024-01-01 15:30:00+05:30\",\"2024-01-02 15:30:00+05:30\")}"`,
		}},
		{"America/New_York", map[string]string{
			"ts":  `"2024-01-01 05:00:00-05"`,
			"arr": `["2024-01-01 05:00:00-05","2024-01-02 05:00:00-05"]`,
			"r":   `"[\"2024-01-01 05:00:00-05\",\"2024-01-02 05:00:00-05\")"`,
			"mr":  `"{[\"2024-01-01 05:00:00-05\",\"2024-01-02 05:00:00-05\")}"`,
		}},
		{"<+05>-05", map[string]string{
			"ts":  `"2024-01-01 15:00:00+05"`,
			"arr": `["2024-01-01 15:00:00+05","2024-01-02 15:00:00+05"]`,
			"r":   `"[\"2024-01-01 15:00:00+05\",\"2024-01-02 15:00:00+05\")"`,
			"mr":  `"{[\"2024-01-01 15:00:00+05\",\"2024-01-02 15:00:00+05\")}"`,
		}},
		{"", map[string]string{
			"ts":  `"2024-01-01 10:00:00+00"`,
			"arr": `["2024-01-01 10:00:00+00","2024-01-02 10:00:00+00"]`,
			"r":   `"[\"2024-01-01 10:00:00+00\",\"2024-01-02 10:00:00+00\")"`,
			"mr":  `"{[\"2024-01-01 10:00:00+00\",\"2024-01-02 10:00:00+00\")}"`,
		}},
		{"Not/AZone", map[string]string{
			"ts": `"2024-01-01 10:00:00+00"`,
		}},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetTimeZone(tc.zone)
			pg.SetResult("SELECT ts, arr, r, mr FROM t", pgfake.Result{Cols: cols, Rows: [][]any{row}, Tag: "SELECT 1"})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT ts, arr, r, mr FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var body struct {
				Rows []map[string]json.RawMessage `json:"rows"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Len(t, body.Rows, 1)
			for col, want := range tc.want {
				assert.JSONEq(t, want, string(body.Rows[0][col]), col)
			}
		})
	}
}

// Raw-text mode passes PostgreSQL's own text through, so the session zone never reaches it.
func TestConformance_TimestamptzRawTextIgnoresSessionZone(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetTimeZone("Asia/Kolkata")
	pg.SetResult("SELECT ts FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "ts", TypeOID: oidTimestamptz}},
		Rows: [][]any{{pgfake.RawColumn("2024-01-01 15:30:00+05:30")}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT ts FROM t"}`, map[string]string{"Neon-Raw-Text-Output": "true"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `"2024-01-01 15:30:00+05:30"`, firstColumn(t, rec.Body.Bytes(), "ts"))
}

func TestTimestamptzText(t *testing.T) {
	m := pgtype.NewMap()
	lmt, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		t    time.Time
		want string
	}{
		{"whole hours", time.Date(2024, 7, 1, 12, 0, 0, 0, time.FixedZone("", 2*3600)), "2024-07-01 12:00:00+02"},
		{"negative minutes", time.Date(2024, 7, 1, 12, 0, 0, 0, time.FixedZone("", -(9*3600+30*60))), "2024-07-01 12:00:00-09:30"},
		{"local mean time seconds", time.Date(1850, 1, 1, 0, 0, 0, 0, lmt), "1850-01-01 00:00:00+05:53:28"},
		{"fraction", time.Date(2024, 1, 1, 0, 0, 0, 500_000_000, time.UTC), "2024-01-01 00:00:00.5+00"},
		{"BC keeps the era last", time.Date(-43, 3, 15, 12, 0, 0, 0, time.FixedZone("", 3600)), "0044-03-15 12:00:00+01 BC"},
		{"time.Local means no session zone", time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC).Local(), "2024-01-01 10:00:00+00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := timestamptzText(m, tc.t)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSessionLocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{"", 0},
		{"UTC", 0},
		{"Etc/UTC", 0},
		{"<+05>-05", 5 * 3600},
		{"<-03:30>+03:30", -(3*3600 + 30*60)},
		{"UTC+5", -5 * 3600},
		{"EST5", -5 * 3600},
		{"Etc/GMT-3", 3 * 3600},
		{"Not/AZone", 0},
		{"Local", 0},
		{"<+05", 0},
		{"UTC+123", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, off := time.Date(2024, 1, 1, 0, 0, 0, 0, sessionLocation(tc.name)).Zone()
			assert.Equal(t, tc.want, off)
		})
	}
	assert.Same(t, time.UTC, sessionLocation("Local"))
}

// Past the cap a zone used to get a fresh *time.Location on every lookup, so
// useSessionTimeZone re-registered the timestamptz types on every query.
func TestSessionLocationStaysCachedPastTheCap(t *testing.T) {
	sessionLocationsMu.Lock()
	saved := sessionLocations
	sessionLocations = map[string]*time.Location{}
	sessionLocationsMu.Unlock()
	t.Cleanup(func() {
		sessionLocationsMu.Lock()
		sessionLocations = saved
		sessionLocationsMu.Unlock()
	})

	for i := range maxSessionLocations + 10 {
		sessionLocation(fmt.Sprintf("<Z%d>-01", i))
	}
	const name = "<+02>-02"
	first := sessionLocation(name)
	assert.Same(t, first, sessionLocation(name))
	sessionLocationsMu.Lock()
	assert.Len(t, sessionLocations, maxSessionLocations)
	sessionLocationsMu.Unlock()

	m := pgtype.NewMap()
	pg := pgfake.New(t)
	pg.SetTimeZone(name)
	conn, err := Dial(context.Background(), Upstream{Addr: pg.Addr(), User: "u", Database: "db", InsecureSkipVerify: true}, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	useSessionTimeZone(m, conn)
	tstz, _ := m.TypeForOID(pgtype.TimestamptzOID)
	useSessionTimeZone(m, conn)
	again, _ := m.TypeForOID(pgtype.TimestamptzOID)
	assert.Same(t, tstz, again, "an unchanged zone must not re-register the types")
}

// A SET TIME ZONE item applies to the timestamptz item after it on every batch
// path, as PostgreSQL itself spells it.
func TestSessionTimeZoneInBatchAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	queries := []QueryItem{
		{Query: "SET TIME ZONE 'Asia/Kolkata'"},
		{Query: "SELECT '2024-01-01 10:00:00+00'::timestamptz AS ts"},
	}
	one := func(string) (int, error) { return 1, nil }
	for _, tc := range []struct {
		name string
		mode pgx.QueryExecMode
		run  func(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context)
	}{
		{"native", pgx.QueryExecModeCacheStatement, func(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context) {
			executeBatch(w, conn, ctx, queries, true, false, txnOptions{}, nil)
		}},
		{"raw text", pgx.QueryExecModeExec, func(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context) {
			executeBatch(w, conn, ctx, queries, true, true, txnOptions{}, nil)
		}},
		{"simple pipeline", pgx.QueryExecModeSimpleProtocol, func(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context) {
			executeBatchSimple(w, conn, ctx, queries, one, true, txnOptions{}, nil)
		}},
		{"simple transaction", pgx.QueryExecModeSimpleProtocol, func(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context) {
			executeBatchSimple(w, conn, ctx, queries, one, true, txnOptions{readOnly: true}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cfg, err := pgx.ParseConfig(url)
			require.NoError(t, err)
			cfg.DefaultQueryExecMode = tc.mode
			cfg.RuntimeParams["timezone"] = "UTC"
			conn, err := pgx.ConnectConfig(ctx, cfg)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close(ctx) })

			rec := httptest.NewRecorder()
			tc.run(rec, conn, ctx)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var body struct {
				Results []struct {
					Rows [][]any `json:"rows"`
				} `json:"results"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Len(t, body.Results, 2)
			require.Len(t, body.Results[1].Rows, 1)
			assert.Equal(t, "2024-01-01 15:30:00+05:30", body.Results[1].Rows[0][0])
		})
	}
}

// Native mode must match PostgreSQL's own text after a SET TIME ZONE on the same session.
func TestSessionTimeZoneAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const query = `SELECT '2024-01-01 10:00:00.5+00'::timestamptz AS ts, ` +
		`ARRAY['1850-01-01 00:00:00+00'::timestamptz] AS arr, ` +
		`tstzrange('2024-07-01 00:00:00+00', NULL) AS r, ROW('2024-01-01 10:00:00+00'::timestamptz) AS rec`
	for _, zone := range []string{"UTC", "Asia/Kolkata", "America/St_Johns", "5"} {
		_, err := conn.Exec(ctx, "SET TIME ZONE '"+zone+"'")
		require.NoError(t, err, zone)
		native, err := QueryRows(ctx, conn, query, nil, true, false)
		require.NoError(t, err, zone)
		text, err := QueryRows(ctx, conn, `SELECT ts::text, arr::text, r::text, rec::text FROM (`+query+`) q`, nil, true, false)
		require.NoError(t, err, zone)
		want := text.Rows[0].([]any)
		row := native.Rows[0].([]any)
		assert.Equal(t, want[0], row[0], zone)
		assert.Equal(t, want[2], row[2], zone)
		assert.Equal(t, want[3], row[3], zone)
		arr, err := json.Marshal(row[1])
		require.NoError(t, err)
		assert.Equal(t, `["`+strings.Trim(want[1].(string), `{}"`)+`"]`, string(arr), zone)
	}
}

// The hosted proxy and cmd/pgserverless run on Alpine with no zoneinfo, so the
// package itself has to link the zone database or every named zone falls back to +00.
func TestPackageEmbedsTimeZoneDatabase(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "sessiontz.go", nil, parser.ImportsOnly)
	require.NoError(t, err)
	var imports []string
	for _, spec := range f.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `"`))
	}
	assert.Contains(t, imports, "time/tzdata")
}
