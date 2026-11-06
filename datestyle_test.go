package pgserverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// The expected strings are what PostgreSQL 17 printed with TimeZone America/Los_Angeles.
func TestStyledDateTimeTextMatchesPostgres(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	date := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	ts := time.Date(2024, 1, 2, 10, 0, 0, 500000000, time.UTC)
	tstz := time.Date(2024, 7, 2, 10, 0, 0, 0, la)
	bcDate := time.Date(-43, 3, 15, 0, 0, 0, 0, time.UTC)
	bcTstz := time.Date(-43, 3, 15, 12, 0, 0, 0, la)
	bigDate := time.Date(12345, 1, 2, 0, 0, 0, 0, time.UTC)
	lmt := time.Date(1850, 1, 1, 0, 0, 0, 0, la)

	tests := []struct {
		style                                    string
		date, ts, tstz, bcDate, bcTstz, big, lmt string
	}{
		{"SQL, MDY", "01/02/2024", "01/02/2024 10:00:00.5", "07/02/2024 10:00:00 PDT", "03/15/0044 BC", "03/15/0044 12:00:00 LMT BC", "01/02/12345", "01/01/1850 00:00:00 LMT"},
		{"SQL, DMY", "02/01/2024", "02/01/2024 10:00:00.5", "02/07/2024 10:00:00 PDT", "15/03/0044 BC", "15/03/0044 12:00:00 LMT BC", "02/01/12345", "01/01/1850 00:00:00 LMT"},
		{"SQL, YMD", "01/02/2024", "01/02/2024 10:00:00.5", "07/02/2024 10:00:00 PDT", "03/15/0044 BC", "03/15/0044 12:00:00 LMT BC", "01/02/12345", "01/01/1850 00:00:00 LMT"},
		{"Postgres, MDY", "01-02-2024", "Tue Jan 02 10:00:00.5 2024", "Tue Jul 02 10:00:00 2024 PDT", "03-15-0044 BC", "Fri Mar 15 12:00:00 0044 LMT BC", "01-02-12345", "Tue Jan 01 00:00:00 1850 LMT"},
		{"Postgres, DMY", "02-01-2024", "Tue 02 Jan 10:00:00.5 2024", "Tue 02 Jul 10:00:00 2024 PDT", "15-03-0044 BC", "Fri 15 Mar 12:00:00 0044 LMT BC", "02-01-12345", "Tue 01 Jan 00:00:00 1850 LMT"},
		{"Postgres, YMD", "01-02-2024", "Tue Jan 02 10:00:00.5 2024", "Tue Jul 02 10:00:00 2024 PDT", "03-15-0044 BC", "Fri Mar 15 12:00:00 0044 LMT BC", "01-02-12345", "Tue Jan 01 00:00:00 1850 LMT"},
		{"German, DMY", "02.01.2024", "02.01.2024 10:00:00.5", "02.07.2024 10:00:00 PDT", "15.03.0044 BC", "15.03.0044 12:00:00 LMT BC", "02.01.12345", "01.01.1850 00:00:00 LMT"},
	}
	for _, tt := range tests {
		t.Run(tt.style, func(t *testing.T) {
			ds, ok := parseDateStyle(tt.style)
			require.True(t, ok)
			assert.Equal(t, tt.date, encodeDateOnly(date, ds))
			assert.Equal(t, tt.ts, encodeDateTime(ts, ds, false))
			assert.Equal(t, tt.tstz, encodeDateTime(tstz, ds, true))
			assert.Equal(t, tt.bcDate, encodeDateOnly(bcDate, ds))
			assert.Equal(t, tt.bcTstz, encodeDateTime(bcTstz, ds, true))
			assert.Equal(t, tt.big, encodeDateOnly(bigDate, ds))
			assert.Equal(t, tt.lmt, encodeDateTime(lmt, ds, true))
		})
	}

	for _, iso := range []string{"ISO, MDY", "ISO, DMY", "", "Nonsense"} {
		_, ok := parseDateStyle(iso)
		assert.False(t, ok, iso)
	}
}

// A zone with no abbreviation falls back to the numeric offset, spaced only in the Postgres style.
func TestStyledDateTimeTextNumericZone(t *testing.T) {
	tstz := time.Date(2024, 1, 2, 10, 0, 0, 0, time.FixedZone("", 5*3600+30*60))
	sql, _ := parseDateStyle("SQL, MDY")
	pgs, _ := parseDateStyle("Postgres, MDY")
	assert.Equal(t, "01/02/2024 10:00:00+05:30", encodeDateTime(tstz, sql, true))
	assert.Equal(t, "Tue Jan 02 10:00:00 2024 +05:30", encodeDateTime(tstz, pgs, true))
}

// A session reporting a non-ISO DateStyle gets date, timestamp and timestamptz,
// their arrays and ranges spelled in it, where native mode always said ISO.
func TestConformance_DatesInSessionDateStyle(t *testing.T) {
	date := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	ts := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	cols := []pgfake.Col{
		{Name: "d", TypeOID: pgtype.DateOID},
		{Name: "ts", TypeOID: pgtype.TimestampOID},
		{Name: "tstz", TypeOID: pgtype.TimestamptzOID},
		{Name: "arr", TypeOID: pgtype.DateArrayOID},
		{Name: "dr", TypeOID: pgtype.DaterangeOID},
	}
	row := []any{
		date, ts, ts,
		[]time.Time{date},
		pgtype.Range[pgtype.Date]{
			Lower: pgtype.Date{Time: date, Valid: true}, Upper: pgtype.Date{Time: date.AddDate(0, 0, 3), Valid: true},
			LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true,
		},
	}

	for _, tc := range []struct {
		style                string
		d, ts, tstz, arr, dr string
	}{
		{"SQL, DMY", `"02/01/2024"`, `"02/01/2024 10:00:00"`, `"02/01/2024 10:00:00 UTC"`, `["02/01/2024"]`, `"[02/01/2024,05/01/2024)"`},
		{"Postgres, MDY", `"01-02-2024"`, `"Tue Jan 02 10:00:00 2024"`, `"Tue Jan 02 10:00:00 2024 UTC"`, `["01-02-2024"]`, `"[01-02-2024,01-05-2024)"`},
		{"German, DMY", `"02.01.2024"`, `"02.01.2024 10:00:00"`, `"02.01.2024 10:00:00 UTC"`, `["02.01.2024"]`, `"[02.01.2024,05.01.2024)"`},
		{"ISO, DMY", `"2024-01-02"`, `"2024-01-02 10:00:00"`, `"2024-01-02 10:00:00+00"`, `["2024-01-02"]`, `"[2024-01-02,2024-01-05)"`},
		{"", `"2024-01-02"`, `"2024-01-02 10:00:00"`, `"2024-01-02 10:00:00+00"`, `["2024-01-02"]`, `"[2024-01-02,2024-01-05)"`},
	} {
		t.Run(tc.style, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetDateStyle(tc.style)
			pg.SetTimeZone("UTC")
			pg.SetResult("SELECT d FROM t", pgfake.Result{Cols: cols, Rows: [][]any{row}, Tag: "SELECT 1"})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT d FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := rec.Body.Bytes()
			assert.JSONEq(t, tc.d, firstColumn(t, body, "d"))
			assert.JSONEq(t, tc.ts, firstColumn(t, body, "ts"))
			assert.JSONEq(t, tc.tstz, firstColumn(t, body, "tstz"))
			assert.JSONEq(t, tc.arr, firstColumn(t, body, "arr"))
			assert.JSONEq(t, tc.dr, firstColumn(t, body, "dr"))
		})
	}
}

// Switching back to ISO restores pgx's own date codec, and an unchanged style re-registers nothing.
func TestUseSessionDateStyleRegistersOnlyOnChange(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetDateStyle("SQL, DMY")
	conn, err := Dial(context.Background(), Upstream{Addr: pg.Addr(), User: "u", Database: "db", InsecureSkipVerify: true}, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	m := pgtype.NewMap()

	useSessionDateStyle(m, conn)
	first, ok := m.TypeForOID(pgtype.DateOID)
	require.True(t, ok)
	assert.IsType(t, &styledDateCodec{}, first.Codec)
	useSessionDateStyle(m, conn)
	again, _ := m.TypeForOID(pgtype.DateOID)
	assert.Same(t, first, again)

	useSessionDateStyle(nil, conn)
	useSessionDateStyle(m, nil)
	still, _ := m.TypeForOID(pgtype.DateOID)
	assert.Same(t, first, still)

	// A styled date type still encodes a parameter as pgx's own codec does.
	buf, err := m.Encode(pgtype.DateOID, pgtype.TextFormatCode, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), nil)
	require.NoError(t, err)
	assert.Equal(t, "2024-01-02", string(buf))
}

// Native mode must match PostgreSQL's own text in every DateStyle, including after a SET in an earlier batch item.
func TestSessionDateStyleAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const values = `(VALUES ('2024-01-02'::date, '2024-01-02 10:00:00.5'::timestamp, '2024-07-02 10:00:00'::timestamptz), ` +
		`('0044-03-15 BC', '0044-03-15 12:00:00.000001 BC', '0044-03-15 12:00 BC'), ` +
		`('12345-01-02', '1850-01-01 00:00', '1850-01-01 00:00'), ` +
		`('infinity', '-infinity', 'infinity')) v(d, ts, tstz)`
	const native = `SELECT d, ts, tstz, ARRAY[d] AS da, ARRAY[ts] AS tsa, ARRAY[tstz] AS tstza, ` +
		`daterange(d, d, '[]') AS dr, tsrange(ts, ts, '[]') AS tsr, tstzrange(tstz, tstz, '[]') AS tstzr, ROW(d, ts, tstz) AS rec FROM ` + values
	const text = `SELECT d::text, ts::text, tstz::text, ARRAY[d]::text, ARRAY[ts]::text, ARRAY[tstz]::text, ` +
		`daterange(d, d, '[]')::text, tsrange(ts, ts, '[]')::text, tstzrange(tstz, tstz, '[]')::text, ROW(d, ts, tstz)::text FROM ` + values
	for _, zone := range []string{"America/Los_Angeles", "Europe/Madrid", "UTC", "Asia/Kolkata"} {
		for _, style := range []string{"SQL, MDY", "SQL, DMY", "Postgres, MDY", "Postgres, DMY", "German", "ISO, DMY", "ISO, MDY"} {
			_, err := conn.Exec(ctx, "SET TIME ZONE '"+zone+"'; SET datestyle = '"+style+"'")
			require.NoError(t, err, style)
			nat, err := QueryRows(ctx, conn, native, nil, true, false)
			require.NoError(t, err, style)
			txt, err := QueryRows(ctx, conn, text, nil, true, true)
			require.NoError(t, err, style)
			require.Len(t, nat.Rows, len(txt.Rows))
			for i := range txt.Rows {
				want, got := txt.Rows[i].([]any), nat.Rows[i].([]any)
				for _, c := range []int{0, 1, 2, 6, 7, 8, 9} {
					assert.Equal(t, want[c], got[c], "%s %s row %d column %d", zone, style, i, c)
				}
				for _, c := range []int{3, 4, 5} {
					arr, err := json.Marshal(got[c])
					require.NoError(t, err)
					var elems []string
					require.NoError(t, json.Unmarshal(arr, &elems))
					require.Len(t, elems, 1)
					scalar, _ := want[c-3].(string)
					assert.Equal(t, scalar, elems[0], "%s %s row %d array %d", zone, style, i, c)
				}
			}
		}
	}

	_, err = conn.Exec(ctx, "SET TIME ZONE 'UTC'; SET datestyle = 'ISO, MDY'")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	executeBatch(rec, conn, ctx, []QueryItem{
		{Query: "SET datestyle = 'German'"},
		{Query: "SELECT '2024-01-02'::date AS d"},
	}, true, false, txnOptions{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			Rows [][]any `json:"rows"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 2)
	assert.Equal(t, "02.01.2024", body.Results[1].Rows[0][0])
}
