package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// time.Time refuses to JSON-marshal a year outside 0 to 9999, and PostgreSQL
// timestamps run from 4713 BC to 294276 AD. One such row used to lose the whole
// response: the status was already written, so the client got a 200 with an
// empty body.
func TestConformance_TimestampOutsideFourDigitYears(t *testing.T) {
	utcClock(t)
	bc := time.Date(-43, 3, 15, 12, 0, 0, 0, time.UTC) // 44 BC
	late := time.Date(10000, 1, 1, 0, 0, 0, 500000000, time.UTC)
	inRange := time.Date(2024, 1, 1, 10, 30, 0, 0, time.UTC)

	pg := pgfake.New(t)
	pg.SetResult("SELECT a, b, c, d FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "a", TypeOID: oidTimestamp},
			{Name: "b", TypeOID: oidTimestamptz},
			{Name: "c", TypeOID: oidTimestampArray},
			{Name: "d", TypeOID: oidTimestamptzArray},
		},
		Rows: [][]any{{
			pgtype.Timestamp{Time: bc, Valid: true},
			pgtype.Timestamptz{Time: late, Valid: true},
			[]pgtype.Timestamp{{Time: bc, Valid: true}, {Time: inRange, Valid: true}},
			[]pgtype.Timestamptz{{Time: late, Valid: true}, {InfinityModifier: pgtype.Infinity, Valid: true}},
		}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a, b, c, d FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw), "body: %q", rec.Body.String())
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"-000043-03-15T12:00:00Z"`, string(rows[0]["a"]))
	assert.JSONEq(t, `"+010000-01-01T00:00:00.5Z"`, string(rows[0]["b"]))
	assert.JSONEq(t, `["-000043-03-15T12:00:00Z","2024-01-01T10:30:00Z"]`, string(rows[0]["c"]))
	assert.JSONEq(t, `["+010000-01-01T00:00:00.5Z","infinity"]`, string(rows[0]["d"]))
}

func TestExpandedYearTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   time.Time
		want string
	}{
		{"oldest timestamp", time.Date(-4712, 1, 1, 0, 0, 0, 0, time.UTC), "-004712-01-01T00:00:00Z"},
		{"2 BC", time.Date(-1, 12, 31, 23, 59, 59, 0, time.UTC), "-000001-12-31T23:59:59Z"},
		{"year 10000", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), "+010000-01-01T00:00:00Z"},
		{
			"latest timestamp keeps offset and microseconds",
			time.Date(294276, 12, 31, 23, 59, 59, 999999000, time.FixedZone("", 3600)),
			"+294276-12-31T23:59:59.999999+01:00",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := expandedYearTimestamp(tc.in)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	for _, in := range []any{
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), // 1 BC, which encoding/json writes as year 0000
		time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		"0044-03-15 12:00:00 BC",
		pgtype.Infinity,
	} {
		_, ok := expandedYearTimestamp(in)
		assert.False(t, ok, "%v", in)
	}
}
