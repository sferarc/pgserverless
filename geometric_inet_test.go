package pgserverless

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// The geometric types and tsvector reach the client as PostgreSQL's own text.
// pgx decodes each of them to a pgtype struct with no MarshalJSON, so
// encoding/json rendered pgtype's field names: an lseg arrived as
// {"P":[{"X":1,"Y":2},{"X":3,"Y":4}],"Valid":true} where PostgreSQL prints
// [(1,2),(3,4)], and a tsvector as a nest of Lexemes whose Weight is the ASCII
// code of the weight letter rather than the letter.
//
// Every expected string here was read off a real PostgreSQL 17.11 as the raw
// wire text for the same literal, not derived from pgx.
func TestConformance_GeometricAndTSVectorAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			"lseg", oidLseg,
			pgtype.Lseg{P: [2]pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Valid: true},
			`"[(1,2),(3,4)]"`,
		},
		{
			// PostgreSQL normalizes a box so the first corner is the upper
			// right, and pgx renders back whatever it decoded, so the corner
			// order the server chose survives.
			"box", oidBox,
			pgtype.Box{P: [2]pgtype.Vec2{{X: 3, Y: 4}, {X: 1, Y: 2}}, Valid: true},
			`"(3,4),(1,2)"`,
		},
		{
			"closed path", oidPath,
			pgtype.Path{P: []pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Closed: true, Valid: true},
			`"((1,2),(3,4))"`,
		},
		{
			// An open path is a different value from a closed one and is
			// printed with square brackets. The struct spelled the difference
			// as a Closed field a client had to know to read.
			"open path", oidPath,
			pgtype.Path{P: []pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Closed: false, Valid: true},
			`"[(1,2),(3,4)]"`,
		},
		{
			"polygon", oidPolygon,
			pgtype.Polygon{P: []pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Valid: true},
			`"((1,2),(3,4))"`,
		},
		{
			"circle", oidCircle,
			pgtype.Circle{P: pgtype.Vec2{X: 1, Y: 2}, R: 3, Valid: true},
			`"<(1,2),3>"`,
		},
		{
			"circle with fractional parts", oidCircle,
			pgtype.Circle{P: pgtype.Vec2{X: 1.5, Y: -2.25}, R: 3.75, Valid: true},
			`"<(1.5,-2.25),3.75>"`,
		},
		{
			"line", oidLine,
			pgtype.Line{A: -1, B: 2, C: -3, Valid: true},
			`"{-1,2,-3}"`,
		},
		{
			// Weight 68 is 'D', the default, which PostgreSQL does not print.
			// Weight 67 is 'C', which it does. The struct gave the client both
			// as bare numbers.
			"tsvector", oidTSVector,
			pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{{
				Word:      "rat",
				Positions: []pgtype.TSVectorPosition{{Position: 4, Weight: pgtype.TSVectorWeightC}, {Position: 5, Weight: pgtype.TSVectorWeightD}},
			}}, Valid: true},
			`"'rat':4C,5"`,
		},
		{
			"tsvector with a space in the lexeme", oidTSVector,
			pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{{
				Word:      "a b",
				Positions: []pgtype.TSVectorPosition{{Position: 1, Weight: pgtype.TSVectorWeightD}},
			}}, Valid: true},
			`"'a b':1"`,
		},
		{
			"tsvector with a quote in the lexeme", oidTSVector,
			pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{{
				Word:      "it's",
				Positions: []pgtype.TSVectorPosition{{Position: 1, Weight: pgtype.TSVectorWeightD}},
			}}, Valid: true},
			`"'it''s':1"`,
		},
		{
			"empty tsvector", oidTSVector,
			pgtype.TSVector{Valid: true},
			`""`,
		},
	} {
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
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["c"]))
		})
	}
}

// point is deliberately untouched: pgtype.Point carries its own MarshalJSON and
// has always returned PostgreSQL's spelling. This pins that, so nobody adds it
// to the coercion switch on the assumption that the whole geometric family was
// broken.
func TestConformance_PointWasAlreadyCorrect(t *testing.T) {
	const oidPoint uint32 = 600
	pg := pgfake.New(t)
	pg.SetResult("SELECT c FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "c", TypeOID: oidPoint}},
		Rows: [][]any{{pgtype.Point{P: pgtype.Vec2{X: 1, Y: 2}, Valid: true}}},
		Tag:  "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT c FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["rows"], &rows))
	require.Len(t, rows, 1)
	assert.JSONEq(t, `"(1,2)"`, string(rows[0]["c"]))
}

// An inet that addresses a single host is printed by PostgreSQL without a
// netmask. pgx decodes inet to netip.Prefix, whose MarshalText always writes
// one, so the column holding 192.168.1.1 reached the client as
// "192.168.1.1/32": a string that looks exactly like a value the column could
// hold, which is why this ranks above the leaked structs above.
//
// A prefix that is not a single host, and every cidr, must come through
// unchanged, because PostgreSQL prints those with the netmask.
func TestConformance_InetHostAddressDropsTheNetmask(t *testing.T) {
	const oidCidr uint32 = 650
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{"ipv4 host", oidInet, netip.MustParsePrefix("192.168.1.1/32"), `"192.168.1.1"`},
		{"ipv6 host", oidInet, netip.MustParsePrefix("2001:db8::1/128"), `"2001:db8::1"`},
		{
			"ipv4-mapped ipv6 host", oidInet,
			netip.MustParsePrefix("::ffff:192.168.1.1/128"),
			`"::ffff:192.168.1.1"`,
		},
		{"ipv4 network", oidInet, netip.MustParsePrefix("192.168.1.0/24"), `"192.168.1.0/24"`},
		{"ipv6 network", oidInet, netip.MustParsePrefix("2001:db8::/64"), `"2001:db8::/64"`},
		{
			// /0 is the far end of the same rule: a netmask of zero bits is
			// still a netmask and PostgreSQL prints it.
			"default route", oidInet, netip.MustParsePrefix("0.0.0.0/0"), `"0.0.0.0/0"`,
		},
		{
			// cidr always carries its netmask in PostgreSQL, including /32, so
			// pgx's spelling was already right and must not be touched.
			"cidr host", oidCidr, netip.MustParsePrefix("10.1.2.3/32"), `"10.1.2.3/32"`,
		},
		{"cidr network", oidCidr, netip.MustParsePrefix("10.0.0.0/8"), `"10.0.0.0/8"`},
	} {
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
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["c"]))
		})
	}
}

// The array forms. pgx decodes each to a []any of the same Go values the scalar
// columns produce, so an uncoerced lseg[] came back as a list of pgtype structs
// and an inet[] as a list of addresses each carrying a /32 nobody wrote.
func TestConformance_GeometricAndInetArrays(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			"lseg array", oidLsegArray,
			[]pgtype.Lseg{
				{P: [2]pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Valid: true},
				{P: [2]pgtype.Vec2{{X: 5, Y: 6}, {X: 7, Y: 8}}, Valid: true},
			},
			`["[(1,2),(3,4)]","[(5,6),(7,8)]"]`,
		},
		{
			// box[] is the one array PostgreSQL punctuates with semicolons
			// rather than commas, which pgx's text array parser cannot read.
			// The JSON path negotiates binary, where the delimiter never
			// appears, so this column does arrive as a slice.
			"box array", oidBoxArray,
			[]pgtype.Box{
				{P: [2]pgtype.Vec2{{X: 3, Y: 4}, {X: 1, Y: 2}}, Valid: true},
				{P: [2]pgtype.Vec2{{X: 7, Y: 8}, {X: 5, Y: 6}}, Valid: true},
			},
			`["(3,4),(1,2)","(7,8),(5,6)"]`,
		},
		{
			"path array", oidPathArray,
			[]pgtype.Path{{P: []pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Closed: true, Valid: true}},
			`["((1,2),(3,4))"]`,
		},
		{
			"polygon array", oidPolygonArray,
			[]pgtype.Polygon{{P: []pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Valid: true}},
			`["((1,2),(3,4))"]`,
		},
		{
			"line array", oidLineArray,
			[]pgtype.Line{{A: 1, B: 2, C: 3, Valid: true}},
			`["{1,2,3}"]`,
		},
		{
			"circle array", oidCircleArray,
			[]pgtype.Circle{{P: pgtype.Vec2{X: 1, Y: 2}, R: 3, Valid: true}},
			`["<(1,2),3>"]`,
		},
		{
			"tsvector array", oidTSVectorArray,
			[]pgtype.TSVector{{Lexemes: []pgtype.TSVectorLexeme{{
				Word:      "a",
				Positions: []pgtype.TSVectorPosition{{Position: 1, Weight: pgtype.TSVectorWeightD}},
			}}, Valid: true}},
			`["'a':1"]`,
		},
		{
			// One host and one network in the same column: only the host loses
			// its netmask.
			"inet array", oidInetArray,
			[]netip.Prefix{
				netip.MustParsePrefix("192.168.1.1/32"),
				netip.MustParsePrefix("10.0.0.0/8"),
			},
			`["192.168.1.1","10.0.0.0/8"]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT a FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "a", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT a FROM t"}`, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["a"]))
		})
	}
}

// A SQL NULL in one of the newly coerced columns stays null. Coercion must not
// invent the empty geometry or the zero address for a value that was not there.
func TestConformance_GeometricAndInetNullStaysNull(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
	}{
		{"lseg", oidLseg},
		{"tsvector", oidTSVector},
		{"inet", oidInet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT c FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "c", TypeOID: tc.typeOID}},
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
			require.Len(t, rows, 1)
			assert.JSONEq(t, `null`, string(rows[0]["c"]))
		})
	}
}

// Raw-text mode never reaches coerceNeonValue, so it was already returning
// PostgreSQL's own text for these columns. This pins the spelling the JSON path
// was made to agree with, so the two paths cannot drift apart.
//
// inet is deliberately not here, and the reason is a limit of the harness rather
// than of the endpoint. The fake backend renders a raw-text column by encoding
// the Go value with pgx's own type map, and pgx's inet encoder is exactly the
// thing that diverges: it writes 192.168.1.1/32 where PostgreSQL's output
// function writes 192.168.1.1. So the fake cannot produce the bytes a real
// server sends for this one type, and asserting against it would pin pgx's
// spelling as if it were PostgreSQL's. The real wire text was read off
// PostgreSQL 17.11 directly and is 192.168.1.1, which is what the JSON path now
// returns. Every other type below round-trips through pgx's encoder exactly, so
// the fake is faithful for them.
func TestConformance_GeometricRawTextUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeOID uint32
		value   any
		want    string
	}{
		{
			"lseg", oidLseg,
			pgtype.Lseg{P: [2]pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Valid: true},
			`"[(1,2),(3,4)]"`,
		},
		{
			"circle", oidCircle,
			pgtype.Circle{P: pgtype.Vec2{X: 1, Y: 2}, R: 3, Valid: true},
			`"<(1,2),3>"`,
		},
		{
			"tsvector", oidTSVector,
			pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{{
				Word:      "a",
				Positions: []pgtype.TSVectorPosition{{Position: 1, Weight: pgtype.TSVectorWeightD}},
			}}, Valid: true},
			`"'a':1"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT c FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "c", TypeOID: tc.typeOID}},
				Rows: [][]any{{tc.value}},
				Tag:  "SELECT 1",
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT c FROM t"}`, map[string]string{
				"Neon-Raw-Text-Output": "true",
			})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw["rows"], &rows))
			require.Len(t, rows, 1)
			assert.JSONEq(t, tc.want, string(rows[0]["c"]))
		})
	}
}

// The unit-level contract for inet, which the end-to-end path cannot reach:
// every shape that must not be touched, and the nil type map (inetHostText does
// not use it, so an inet host is still coerced without one, unlike the pgTextValue
// family).
func TestInetHostText(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
		ok   bool
	}{
		{"ipv4 host", netip.MustParsePrefix("192.168.1.1/32"), "192.168.1.1", true},
		{"ipv6 host", netip.MustParsePrefix("2001:db8::1/128"), "2001:db8::1", true},
		{"ipv4 network", netip.MustParsePrefix("192.168.1.0/24"), "", false},
		{"ipv6 network", netip.MustParsePrefix("2001:db8::/64"), "", false},
		{"default route", netip.MustParsePrefix("0.0.0.0/0"), "", false},
		{"ipv6 default route", netip.MustParsePrefix("::/0"), "", false},
		{"zero prefix", netip.Prefix{}, "", false},
		{"not a prefix", "192.168.1.1", "", false},
		{"an addr rather than a prefix", netip.MustParseAddr("192.168.1.1"), "", false},
		{"nil", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := inetHostText(tc.in)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	// A value that already arrived as PostgreSQL text (the simple protocol, or a
	// type pgx has no codec for) is left exactly as it came.
	assert.Equal(t, any("192.168.1.1"), coerceNeonValue(pgtype.NewMap(), "192.168.1.1", oidInet))
	assert.Equal(t, any("192.168.1.1"), coerceNeonValue(nil, netip.MustParsePrefix("192.168.1.1/32"), oidInet))
	assert.Equal(t, any(netip.MustParsePrefix("10.0.0.0/8")), coerceNeonValue(nil, netip.MustParsePrefix("10.0.0.0/8"), oidInet))
}

// The nil type map, which is the contract pgTextValue has always held for the
// types it covers: without a map they pass through unchanged rather than the
// call failing or the column being dropped. tsvector is rendered by tsvectorText,
// which needs no map, so it is coerced either way.
func TestCoerceNeonValue_GeometricWithoutATypeMap(t *testing.T) {
	m := pgtype.NewMap()
	lseg := pgtype.Lseg{P: [2]pgtype.Vec2{{X: 1, Y: 2}, {X: 3, Y: 4}}, Valid: true}
	tsv := pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{{
		Word:      "a",
		Positions: []pgtype.TSVectorPosition{{Position: 1, Weight: pgtype.TSVectorWeightD}},
	}}, Valid: true}

	assert.Equal(t, any("[(1,2),(3,4)]"), coerceNeonValue(m, lseg, oidLseg))
	assert.Equal(t, any("'a':1"), coerceNeonValue(m, tsv, oidTSVector))

	assert.Equal(t, any(lseg), coerceNeonValue(nil, lseg, oidLseg))
	assert.Equal(t, any("'a':1"), coerceNeonValue(nil, tsv, oidTSVector))
}

// A tsvector lexeme is spelled the way PostgreSQL's tsvectorout spells it: an
// embedded quote and an embedded backslash are both doubled. pgx's own encode
// backslash-escapes the quote, which parses back to the same lexeme but is not
// the text PostgreSQL, and so @neondatabase/serverless, returns.
//
// Each expected string with a SELECT above it was read off a real PostgreSQL
// 17.11 as the text output of that literal.
func TestTSVectorTextMatchesPostgres(t *testing.T) {
	lexeme := func(word string, positions ...pgtype.TSVectorPosition) pgtype.TSVectorLexeme {
		return pgtype.TSVectorLexeme{Word: word, Positions: positions}
	}
	pos := func(n uint16, w pgtype.TSVectorWeight) pgtype.TSVectorPosition {
		return pgtype.TSVectorPosition{Position: n, Weight: w}
	}

	for _, tc := range []struct {
		name string
		in   any
		want string
		ok   bool
	}{
		// SELECT $$'c''d':2$$::tsvector
		{"quote", pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{lexeme("c'd", pos(2, pgtype.TSVectorWeightD))}, Valid: true}, `'c''d':2`, true},
		// SELECT $$'c\\d':2$$::tsvector
		{"backslash", pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{lexeme(`c\d`, pos(2, pgtype.TSVectorWeightD))}, Valid: true}, `'c\\d':2`, true},
		// SELECT $$'it''s' 'a\\''b':1A,3$$::tsvector
		{
			"quote and backslash across lexemes",
			pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{
				lexeme(`a\'b`, pos(1, pgtype.TSVectorWeightA), pos(3, pgtype.TSVectorWeightD)),
				lexeme("it's"),
			}, Valid: true},
			`'a\\''b':1A,3 'it''s'`, true,
		},
		{"no positions", pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{lexeme("a")}, Valid: true}, `'a'`, true},
		{"unset weight is D", pgtype.TSVector{Lexemes: []pgtype.TSVectorLexeme{lexeme("a", pos(1, 0))}, Valid: true}, `'a':1`, true},
		{"empty", pgtype.TSVector{Valid: true}, ``, true},
		{"invalid", pgtype.TSVector{}, ``, false},
		{"already text", "'a':1", ``, false},
		{"nil", nil, ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tsvectorText(tc.in)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	// Text that already arrived as a string (the simple protocol) is left exactly
	// as it came, however it is escaped.
	assert.Equal(t, any(`'c\'d':2`), coerceNeonValue(pgtype.NewMap(), `'c\'d':2`, oidTSVector))
}
