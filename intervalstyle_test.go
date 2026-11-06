package pgserverless

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func iv(months, days int32, usec int64) pgtype.Interval {
	return pgtype.Interval{Months: months, Days: days, Microseconds: usec, Valid: true}
}

// The expected strings are what PostgreSQL 17's interval_out printed for each
// value under each IntervalStyle.
func TestIntervalTextInMatchesPostgres(t *testing.T) {
	const h, m, s = int64(usecPerHour), int64(usecPerMinute), int64(usecPerSecond)
	tests := []struct {
		name                   string
		v                      pgtype.Interval
		sql, iso, verbose, pgs string
	}{
		{"zero", iv(0, 0, 0), "0", "PT0S", "@ 0", "00:00:00"},
		{"one day", iv(0, 1, 0), "1 0:00:00", "P1D", "@ 1 day", "1 day"},
		{"minus one day", iv(0, -1, 0), "-1 0:00:00", "P-1D", "@ 1 day ago", "-1 days"},
		{"year and months", iv(14, 0, 0), "1-2", "P1Y2M", "@ 1 year 2 mons", "1 year 2 mons"},
		{"negative year and months", iv(-14, 0, 0), "-1-2", "P-1Y-2M", "@ 1 year 2 mons ago", "-1 years -2 mons"},
		{"hours", iv(0, 0, 2*h), "2:00:00", "PT2H", "@ 2 hours", "02:00:00"},
		{"negative hours", iv(0, 0, -2*h), "-2:00:00", "PT-2H", "@ 2 hours ago", "-02:00:00"},
		{"day and clock", iv(0, 1, 2*h+3*m+4*s+500000), "1 2:03:04.5", "P1DT2H3M4.5S", "@ 1 day 2 hours 3 mins 4.5 secs", "1 day 02:03:04.5"},
		{"positive time after a negative day", iv(0, -1, 2*h), "+0-0 -1 +2:00:00", "P-1DT2H", "@ 1 day -2 hours ago", "-1 days +02:00:00"},
		{"negative time after a positive day", iv(0, 1, -2*h), "+0-0 +1 -2:00:00", "P1DT-2H", "@ 1 day -2 hours", "1 day -02:00:00"},
		{"every field mixed", iv(14, -3, 4*h+5*m+6*s+789000), "+1-2 -3 +4:05:06.789", "P1Y2M-3DT4H5M6.789S", "@ 1 year 2 mons -3 days 4 hours 5 mins 6.789 secs", "1 year 2 mons -3 days +04:05:06.789"},
		{"negative fraction", iv(0, 0, -500000), "-0:00:00.5", "PT-0.5S", "@ 0.5 secs ago", "-00:00:00.5"},
		{"one second", iv(0, 0, s), "0:00:01", "PT1S", "@ 1 sec", "00:00:01"},
		{"minus one second", iv(0, 0, -s), "-0:00:01", "PT-1S", "@ 1 sec ago", "-00:00:01"},
		{"one microsecond", iv(0, 0, 1), "0:00:00.000001", "PT0.000001S", "@ 0.000001 secs", "00:00:00.000001"},
		{"hours past a day", iv(0, 0, 100*h), "100:00:00", "PT100H", "@ 100 hours", "100:00:00"},
		{"positive day after a negative month", iv(-1, 1, 0), "-0-1 +1 +0:00:00", "P-1M1D", "@ 1 mon -1 days ago", "-1 mons +1 day"},
		{"year and days", iv(12, 2, 0), "+1-0 +2 +0:00:00", "P1Y2D", "@ 1 year 2 days", "1 year 2 days"},
		{"all negative day time", iv(0, -3, -2*h-500000), "-3 2:00:00.5", "P-3DT-2H-0.5S", "@ 3 days 2 hours 0.5 secs ago", "-3 days -02:00:00.5"},
		{"every field negative", iv(-14, -3, -4*h-5*m-6*s), "-1-2 -3 -4:05:06", "P-1Y-2M-3DT-4H-5M-6S", "@ 1 year 2 mons 3 days 4 hours 5 mins 6 secs ago", "-1 years -2 mons -3 days -04:05:06"},
		{"negative seconds after a day", iv(0, 1, -5*s), "+0-0 +1 -0:00:05", "P1DT-5S", "@ 1 day -5 secs", "1 day -00:00:05"},
		{"one of each clock unit", iv(0, 0, h+m+s), "1:01:01", "PT1H1M1S", "@ 1 hour 1 min 1 sec", "01:01:01"},
		{"infinity", iv(math.MaxInt32, math.MaxInt32, math.MaxInt64), "infinity", "infinity", "infinity", "infinity"},
		{"minus infinity", iv(math.MinInt32, math.MinInt32, math.MinInt64), "-infinity", "-infinity", "-infinity", "-infinity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.sql, intervalTextIn(tt.v, intervalStyleSQLStandard), "sql_standard")
			assert.Equal(t, tt.iso, intervalTextIn(tt.v, intervalStyleISO8601), "iso_8601")
			assert.Equal(t, tt.verbose, intervalTextIn(tt.v, intervalStylePostgresVerbose), "postgres_verbose")
			assert.Equal(t, tt.pgs, intervalTextIn(tt.v, intervalStylePostgres), "postgres")
		})
	}
}

// A session reporting a non-default IntervalStyle gets intervals and interval
// arrays spelled in it, where native mode always said postgres.
func TestConformance_IntervalInSessionIntervalStyle(t *testing.T) {
	day2h := pgtype.Interval{Days: 1, Microseconds: 2 * usecPerHour, Valid: true}
	cols := []pgfake.Col{
		{Name: "i", TypeOID: pgtype.IntervalOID},
		{Name: "arr", TypeOID: pgtype.IntervalArrayOID},
	}
	row := []any{day2h, []pgtype.Interval{day2h, {Months: 14, Valid: true}}}

	for _, tc := range []struct {
		style  string
		i, arr string
	}{
		{"iso_8601", `"P1DT2H"`, `["P1DT2H","P1Y2M"]`},
		{"sql_standard", `"1 2:00:00"`, `["1 2:00:00","1-2"]`},
		{"postgres_verbose", `"@ 1 day 2 hours"`, `["@ 1 day 2 hours","@ 1 year 2 mons"]`},
		{"postgres", `"1 day 02:00:00"`, `["1 day 02:00:00","1 year 2 mons"]`},
		{"", `"1 day 02:00:00"`, `["1 day 02:00:00","1 year 2 mons"]`},
	} {
		t.Run(tc.style, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetIntervalStyle(tc.style)
			pg.SetResult("SELECT i, arr FROM t", pgfake.Result{Cols: cols, Rows: [][]any{row}, Tag: "SELECT 1"})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT i, arr FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.JSONEq(t, tc.i, firstColumn(t, rec.Body.Bytes(), "i"))
			assert.JSONEq(t, tc.arr, firstColumn(t, rec.Body.Bytes(), "arr"))
		})
	}
}

// Switching back to postgres restores pgx's own interval codec, and an unchanged style re-registers nothing.
func TestUseSessionIntervalStyleRegistersOnlyOnChange(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetIntervalStyle("iso_8601")
	conn, err := Dial(context.Background(), Upstream{Addr: pg.Addr(), User: "u", Database: "db", InsecureSkipVerify: true}, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	m := pgtype.NewMap()

	useSessionIntervalStyle(m, conn)
	first, ok := m.TypeForOID(pgtype.IntervalOID)
	require.True(t, ok)
	assert.IsType(t, &styledIntervalCodec{}, first.Codec)
	useSessionIntervalStyle(m, conn)
	again, _ := m.TypeForOID(pgtype.IntervalOID)
	assert.Same(t, first, again)

	useSessionIntervalStyle(nil, conn)
	useSessionIntervalStyle(m, nil)
	still, _ := m.TypeForOID(pgtype.IntervalOID)
	assert.Same(t, first, still)
}

// Native mode must match PostgreSQL's own text in every IntervalStyle, including after a SET in an earlier batch item.
func TestSessionIntervalStyleAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const values = `(VALUES ('0'::interval), ('1 day -2 hours'), ('-1 year -2 mons'), ('1 year 2 mons -3 days 4:05:06.789'), ` +
		`('-0.5 sec'), ('-1 day +5 sec'), ('-1 mon +1 day'), ('100 hours'), ('infinity'), ('-infinity')) v(x)`
	for _, style := range []string{"sql_standard", "iso_8601", "postgres_verbose", "postgres"} {
		_, err := conn.Exec(ctx, "SET intervalstyle = "+style)
		require.NoError(t, err, style)
		native, err := QueryRows(ctx, conn, `SELECT x, ARRAY[x] AS arr, ROW(x) AS rec FROM `+values, nil, true, false)
		require.NoError(t, err, style)
		text, err := QueryRows(ctx, conn, `SELECT x::text, ARRAY[x]::text, ROW(x)::text FROM `+values, nil, true, true)
		require.NoError(t, err, style)
		require.Len(t, native.Rows, len(text.Rows))
		for i := range text.Rows {
			want, got := text.Rows[i].([]any), native.Rows[i].([]any)
			assert.Equal(t, want[0], got[0], style)
			assert.Equal(t, want[2], got[2], style)
			arr, err := json.Marshal(got[1])
			require.NoError(t, err)
			var elems []string
			require.NoError(t, json.Unmarshal(arr, &elems))
			require.Len(t, elems, 1)
			assert.Equal(t, want[0], elems[0], style)
		}
	}

	_, err = conn.Exec(ctx, "SET intervalstyle = postgres")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	executeBatch(rec, conn, ctx, []QueryItem{
		{Query: "SET intervalstyle = iso_8601"},
		{Query: "SELECT '1 day 2 hours'::interval AS i"},
	}, true, false, txnOptions{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			Rows [][]any `json:"rows"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 2)
	assert.Equal(t, "P1DT2H", body.Results[1].Rows[0][0])
}
