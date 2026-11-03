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

// infinity and -infinity are ordinary values of date, timestamp and timestamptz,
// and PostgreSQL prints them as those two words. pgx does not decode them to a
// time.Time, because there is no such instant: it decodes them to
// pgtype.InfinityModifier, which is an int8 enum, so encoding/json rendered
// infinity as 1 and -infinity as -1. A client reading a timestamp column got the
// number 1 with nothing saying it was not the column's value, which is the worst
// version of this family of bugs: a leaked struct at least looks foreign, while 1
// looks like data.
func TestConformance_InfiniteTimestampsAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			"timestamp infinity", oidTimestamp,
			pgtype.Timestamp{InfinityModifier: pgtype.Infinity, Valid: true},
			`"infinity"`,
		},
		{
			"timestamp -infinity", oidTimestamp,
			pgtype.Timestamp{InfinityModifier: pgtype.NegativeInfinity, Valid: true},
			`"-infinity"`,
		},
		{
			"timestamptz infinity", oidTimestamptz,
			pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true},
			`"infinity"`,
		},
		{
			"timestamptz -infinity", oidTimestamptz,
			pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true},
			`"-infinity"`,
		},
		{
			// date already came back as text, because it is one of the OIDs
			// pgTextValue covers. Pinned so the two spellings cannot drift.
			"date infinity", oidDate,
			pgtype.Date{InfinityModifier: pgtype.Infinity, Valid: true},
			`"infinity"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT ts FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "ts", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT ts FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["ts"]))
		})
	}
}

// The array form. pgx decodes timestamp[] to a []any and hands back the same
// InfinityModifier for an infinite element, so an array carrying one came back
// with a 1 sitting between the timestamps. date[] was already right because
// oidDateArray was in neonArrayElemOID; the two timestamp array types were not.
// utcClock pins the process clock's zone for one test.
//
// pgx hands back a timestamptz whose zone follows the process. The rendering
// normalizes it to UTC before writing "+00", so the spelling should not depend
// on the host, but the clock is pinned anyway so a regression there shows up as
// a wrong spelling on every machine rather than only off CI.
func utcClock(t *testing.T) {
	t.Helper()
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
}

func TestConformance_InfiniteTimestampsInArray(t *testing.T) {
	utcClock(t)
	finite := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    []string
	}{
		{
			"timestamp array", oidTimestampArray,
			[]pgtype.Timestamp{
				{InfinityModifier: pgtype.Infinity, Valid: true},
				{Time: finite, Valid: true},
				{InfinityModifier: pgtype.NegativeInfinity, Valid: true},
			},
			[]string{"infinity", "2024-01-01 00:00:00", "-infinity"},
		},
		{
			"timestamptz array", oidTimestamptzArray,
			[]pgtype.Timestamptz{
				{InfinityModifier: pgtype.Infinity, Valid: true},
				{Time: finite, Valid: true},
			},
			[]string{"infinity", "2024-01-01 00:00:00+00"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT ts FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "ts", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT ts FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assertTimestampElems(t, tc.want, rows[0]["ts"])
		})
	}
}

// assertTimestampElems compares a JSON array of timestamp strings element by
// element, as the exact words PostgreSQL prints.
func assertTimestampElems(t *testing.T, want []string, got json.RawMessage) {
	t.Helper()
	var elems []string
	require.NoError(t, json.Unmarshal(got, &elems))
	assert.Equal(t, want, elems)
}

// A finite timestamp is spelled as PostgreSQL prints it in a UTC session, which
// is what Neon's pg_text_to_json returns: every scalar that is not a bool, an
// int2 or int4, a float or json goes out as its PostgreSQL text. This used to
// be RFC 3339 ("2024-01-01T10:30:00Z"), which put a "Z" on a timestamp that
// carries no zone. SPEC Appendix B.2 records the decision.
func TestConformance_FiniteTimestampsAsText(t *testing.T) {
	utcClock(t)
	finite := time.Date(2024, 1, 1, 10, 30, 0, 0, time.UTC)
	pg := pgfake.New(t)
	pg.SetResult("SELECT a, b, c FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "a", TypeOID: oidTimestamp},
			{Name: "b", TypeOID: oidTimestamptz},
			{Name: "c", TypeOID: oidTimestampArray},
		},
		Rows: [][]any{{
			pgtype.Timestamp{Time: finite, Valid: true},
			pgtype.Timestamptz{Time: finite, Valid: true},
			[]pgtype.Timestamp{{Time: finite, Valid: true}},
		}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a, b, c FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"2024-01-01 10:30:00"`, string(rows[0]["a"]))
	assert.JSONEq(t, `"2024-01-01 10:30:00+00"`, string(rows[0]["b"]))
	assert.JSONEq(t, `["2024-01-01 10:30:00"]`, string(rows[0]["c"]))
}

// pgx decodes a timestamptz into time.Local, so on a host outside UTC the value
// carries that host's offset. The spelling must not: it is the instant in UTC,
// as PostgreSQL prints it in the UTC session Neon and the reference server use.
func TestConformance_TimestamptzTextIgnoresProcessZone(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("CET", 3600)
	t.Cleanup(func() { time.Local = previous })

	at := time.Date(2024, 6, 1, 10, 0, 0, 123456000, time.UTC)
	pg := pgfake.New(t)
	pg.SetResult("SELECT a, b FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "a", TypeOID: oidTimestamptz},
			{Name: "b", TypeOID: oidTimestamptzArray},
		},
		Rows: [][]any{{
			pgtype.Timestamptz{Time: at, Valid: true},
			[]pgtype.Timestamptz{{Time: at, Valid: true}},
		}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT a, b FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"2024-06-01 10:00:00.123456+00"`, string(rows[0]["a"]))
	assert.JSONEq(t, `["2024-06-01 10:00:00.123456+00"]`, string(rows[0]["b"]))
}

// Raw-text mode never reaches coerceNeonValue, so it was never broken: it
// already returned PostgreSQL's own words. This pins the spelling the JSON path
// was made to agree with, so the two paths cannot drift apart again.
func TestConformance_InfiniteTimestampRawTextUnchanged(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT ts FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "ts", TypeOID: oidTimestamptz}},
		Rows: [][]any{{pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT ts FROM t"}`, map[string]string{
		"Neon-Raw-Text-Output": "true",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"infinity"`, string(rows[0]["ts"]))
}

// The unit-level contract of the helper: exactly the two infinite values are
// rendered, and every other value pgx can hand back is left alone. Finite is in
// the list because pgtype's zero value carries it, and its String() is "finite",
// which must never reach a response body.
func TestInfinityText(t *testing.T) {
	got, ok := infinityText(pgtype.Infinity)
	require.True(t, ok)
	assert.Equal(t, "infinity", got)

	got, ok = infinityText(pgtype.NegativeInfinity)
	require.True(t, ok)
	assert.Equal(t, "-infinity", got)

	for _, v := range []any{
		pgtype.Finite,
		nil, int8(1), int64(1), 1.0, "infinity", true,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		[]any{pgtype.Infinity},
	} {
		_, ok := infinityText(v)
		assert.False(t, ok, "%#v must not be coerced", v)
	}
}
