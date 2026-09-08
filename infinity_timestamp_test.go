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
// A finite timestamp is marshalled by Go in the zone its time.Time carries, and
// pgx hands back a value whose zone follows the process. So these assertions
// compared an exact string against a rendering that changes with the developer's
// machine: `2024-01-01T10:30:00Z` on a UTC host, `2024-01-01T11:30:00+01:00` in
// Madrid. Both are RFC 3339 and both are the same instant, so the endpoint was
// never wrong; the tests were, and they failed for everyone outside UTC while
// passing in CI, which is the worst way round.
//
// Pinned rather than made offset-agnostic because the wire format is the thing
// under test: the compat matrix promises "the RFC 3339 instants this endpoint has
// always returned", and a test that accepts any offset stops checking the format.
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
			[]string{"infinity", "2024-01-01T00:00:00Z", "-infinity"},
		},
		{
			"timestamptz array", oidTimestamptzArray,
			[]pgtype.Timestamptz{
				{InfinityModifier: pgtype.Infinity, Valid: true},
				{Time: finite, Valid: true},
			},
			[]string{"infinity", "2024-01-01T00:00:00Z"},
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
// element. An element that parses as RFC 3339 is compared as an instant rather
// than as a spelling: pgx decodes a timestamptz into time.Local, so
// encoding/json writes whatever offset the process's zone has, "…T00:00:00Z" in
// the UTC containers this runs in and "…T01:00:00+01:00" on a developer machine
// in CET. Both are the same instant and both are RFC 3339. The infinite elements
// are what this test is about and are compared as the exact words PostgreSQL
// prints.
func assertTimestampElems(t *testing.T, want []string, got json.RawMessage) {
	t.Helper()
	var elems []string
	require.NoError(t, json.Unmarshal(got, &elems))
	require.Len(t, elems, len(want))
	for i, w := range want {
		wantAt, err := time.Parse(time.RFC3339, w)
		if err != nil {
			assert.Equal(t, w, elems[i])
			continue
		}
		gotAt, err := time.Parse(time.RFC3339, elems[i])
		require.NoError(t, err, "element %d: %q is not RFC 3339", i, elems[i])
		assert.True(t, wantAt.Equal(gotAt), "element %d: %s is not the instant %s", i, elems[i], w)
	}
}

// The regression guard on the other side. A finite timestamp is not touched: it
// stays the RFC 3339 instant this endpoint has always returned, which the compat
// matrix records as a deliberate divergence from PostgreSQL's own text. Coercing
// every timestamp to PostgreSQL text would be a silent contract change for every
// client already parsing these columns as dates.
func TestConformance_FiniteTimestampsUnchanged(t *testing.T) {
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
	// timestamp and timestamp[] carry no zone, so pgx decodes them in UTC and
	// the spelling is fixed.
	assert.JSONEq(t, `"2024-01-01T10:30:00Z"`, string(rows[0]["a"]))
	assert.JSONEq(t, `["2024-01-01T10:30:00Z"]`, string(rows[0]["c"]))

	// timestamptz is asserted as an instant rather than as a spelling. pgx
	// decodes it into time.Local, so encoding/json writes whatever offset the
	// process's zone has: "…T10:30:00Z" in the UTC containers this runs in and
	// "…T11:30:00+01:00" on a developer machine in CET. Both are the same
	// instant and both are RFC 3339, which is the whole of what this test is
	// guarding, so pinning the offset would only make it fail off-CI.
	var tz time.Time
	require.NoError(t, json.Unmarshal(rows[0]["b"], &tz))
	assert.True(t, finite.Equal(tz), "timestamptz %s is not the instant %s", tz, finite)
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
