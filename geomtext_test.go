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

// Each expected string was read off PostgreSQL 17.11 (extra_float_digits 1) for the literal in the comment above it.
func TestGeometryTextMatchesPostgres(t *testing.T) {
	v := func(x, y float64) pgtype.Vec2 { return pgtype.Vec2{X: x, Y: y} }

	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		// SELECT point(1234567,0.00001)
		{"point past %g precision stays digits", pgtype.Point{P: v(1234567, 0.00001), Valid: true}, "(1234567,1e-05)"},
		// SELECT point(1e20,-1e-5)
		{"point exponent form", pgtype.Point{P: v(1e20, -1e-5), Valid: true}, "(1e+20,-1e-05)"},
		// SELECT point('Infinity','-Infinity'), point('NaN',1.5)
		{"point infinities", pgtype.Point{P: v(math.Inf(1), math.Inf(-1)), Valid: true}, "(Infinity,-Infinity)"},
		{"point NaN", pgtype.Point{P: v(math.NaN(), 1.5), Valid: true}, "(NaN,1.5)"},
		// SELECT box(point(1e20,1e-5),point(0,0))
		{"box", pgtype.Box{P: [2]pgtype.Vec2{v(1e20, 1e-5), v(0, 0)}, Valid: true}, "(1e+20,1e-05),(0,0)"},
		// SELECT lseg(point(1234567890123456,1),point(123456789012345,2))
		{"lseg at DBL_DIG", pgtype.Lseg{P: [2]pgtype.Vec2{v(1234567890123456, 1), v(123456789012345, 2)}, Valid: true}, "[(1.234567890123456e+15,1),(123456789012345,2)]"},
		// SELECT '[(1e16,0),(1,1)]'::path
		{"open path", pgtype.Path{P: []pgtype.Vec2{v(1e16, 0), v(1, 1)}, Valid: true}, "[(1e+16,0),(1,1)]"},
		{"closed path", pgtype.Path{P: []pgtype.Vec2{v(1, 2), v(3, 4)}, Closed: true, Valid: true}, "((1,2),(3,4))"},
		// SELECT '((1e-7,0),(1,1),(2,0))'::polygon
		{"polygon", pgtype.Polygon{P: []pgtype.Vec2{v(1e-7, 0), v(1, 1), v(2, 0)}, Valid: true}, "((1e-07,0),(1,1),(2,0))"},
		// SELECT line(point(0,0),point(1e20,1))
		{"line", pgtype.Line{A: 1e-20, B: -1, C: 0, Valid: true}, "{1e-20,-1,0}"},
		// SELECT circle(point(1e15,1e-4),1e100)
		{"circle", pgtype.Circle{P: v(1e15, 1e-4), R: 1e100, Valid: true}, "<(1e+15,0.0001),1e+100>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := geometryText(tc.in)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	for _, in := range []any{pgtype.Point{}, pgtype.Lseg{}, pgtype.Box{}, pgtype.Path{}, pgtype.Polygon{}, pgtype.Line{}, pgtype.Circle{}, "(1,2)", nil} {
		_, ok := geometryText(in)
		assert.False(t, ok, "%#v", in)
	}
}

var geomOIDs = []uint32{
	pgtype.PointOID, pgtype.LsegOID, pgtype.PathOID, pgtype.BoxOID, pgtype.PolygonOID, pgtype.LineOID, pgtype.CircleOID,
	pgtype.PointArrayOID, pgtype.LsegArrayOID, pgtype.PathArrayOID, pgtype.BoxArrayOID, pgtype.PolygonArrayOID, pgtype.LineArrayOID, pgtype.CircleArrayOID,
}

// Under extra_float_digits = 0 PostgreSQL prints point(0.1::float8 + 0.2, 1) as
// (0.3,1), which Neon returns as it came; the fake answers in that spelling only
// if the handler asked for text, since a binary column would carry the stored value.
func TestConformance_GeometricInSessionExtraFloatDigits(t *testing.T) {
	cols := []pgfake.Col{
		{Name: "p", TypeOID: oidPoint},
		{Name: "c", TypeOID: oidCircle},
		{Name: "pa", TypeOID: oidPointArray},
		{Name: "ba", TypeOID: oidBoxArray},
	}
	pg := pgfake.New(t)
	pg.SetResult("SELECT p, c, pa, ba FROM t", pgfake.Result{
		Cols: cols,
		Rows: [][]any{{
			pgfake.RawColumn(`(0.3,1)`),
			pgfake.RawColumn(`<(0,0),0.3>`),
			pgfake.RawColumn(`{"(0.3,1)",NULL}`),
			pgfake.RawColumn(`{{(0.3,1),(0,0);NULL};{(2,2),(1,1);(1,1),(0,0)}}`),
		}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT p, c, pa, ba FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `"(0.3,1)"`, firstColumn(t, rec.Body.Bytes(), "p"))
	assert.JSONEq(t, `"<(0,0),0.3>"`, firstColumn(t, rec.Body.Bytes(), "c"))
	assert.JSONEq(t, `["(0.3,1)",null]`, firstColumn(t, rec.Body.Bytes(), "pa"))
	assert.JSONEq(t, `[["(0.3,1),(0,0)",null],["(2,2),(1,1)","(1,1),(0,0)"]]`, firstColumn(t, rec.Body.Bytes(), "ba"))
}

func TestUseTextGeomAsksForText(t *testing.T) {
	m := pgtype.NewMap()
	useTextGeom(m)
	for _, oid := range geomOIDs {
		assert.Equal(t, int16(pgtype.TextFormatCode), m.FormatCodeForOID(oid), oid)
	}

	v, err := m.Encode(pgtype.BoxArrayOID, pgtype.TextFormatCode, []pgtype.Box{{P: [2]pgtype.Vec2{{X: 1, Y: 1}, {X: 0, Y: 0}}, Valid: true}, {}}, nil)
	require.NoError(t, err, "a box[] parameter still encodes")
	assert.Equal(t, `{"(1,1),(0,0)";NULL}`, string(v), "box[] keeps its semicolon delimiter")
	point, ok := m.TypeForOID(pgtype.PointOID)
	require.True(t, ok)
	dec, err := point.Codec.DecodeValue(m, pgtype.PointOID, pgtype.TextFormatCode, []byte("(0.3,1)"))
	require.NoError(t, err)
	assert.Equal(t, "(0.3,1)", dec, "point text keeps the digits PostgreSQL printed")

	useTextGeom(m)
	again, _ := m.TypeForOID(pgtype.PointOID)
	assert.Same(t, point, again, "a second call registers nothing")
	useTextGeom(nil)
}

func TestTextGeomArrayElems(t *testing.T) {
	elems, ok := textGeomArrayElems(`{{(1,1),(0,0);NULL};{(2,2),(1,1);(3,3),(2,2)}}`, oidBoxArray)
	require.True(t, ok)
	assert.Equal(t, []any{[]any{"(1,1),(0,0)", nil}, []any{"(2,2),(1,1)", "(3,3),(2,2)"}}, elems)

	elems, ok = textGeomArrayElems(`{"{1,2,3}","<(1,2),3>",NULL}`, oidLineArray)
	require.True(t, ok)
	assert.Equal(t, []any{"{1,2,3}", "<(1,2),3>", nil}, elems)

	_, ok = textGeomArrayElems(`{"(1,2)"}`, oidFloat8Array)
	assert.False(t, ok, "not a geometric array")
	_, ok = textGeomArrayElems([]any{"(1,2)"}, oidPointArray)
	assert.False(t, ok, "an already decoded array is left to the caller")
	_, ok = textGeomArrayElems(`{(1,1),(0,0)`, oidBoxArray)
	assert.False(t, ok, "an unterminated literal")
}

// Checked against a real PostgreSQL, whose text Neon returns as it came, under
// extra_float_digits 0, 1 (the default) and 3, and after a SET inside a native batch.
func TestExtraFloatDigitsGeometricAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const x = `(0.1::float8 + 0.2)`
	values := []string{
		`point(` + x + `, 1e20)`,
		`lseg(point(` + x + `, 1), point(1e-5, 2))`,
		`path(polygon(box(point(` + x + `, 1), point(0, 0))))`,
		`popen(path(polygon(box(point(` + x + `, 1), point(0, 0)))))`,
		`box(point(` + x + `, 1), point(0, 0))`,
		`polygon(box(point(` + x + `, 1), point(0, 0)))`,
		`line(point(0, 0), point(` + x + `, 1))`,
		`circle(point(0, 0), ` + x + `)`,
	}
	for _, digits := range []string{"0", "1", "3"} {
		_, err := conn.Exec(ctx, "SET extra_float_digits = "+digits)
		require.NoError(t, err, digits)
		for _, v := range values {
			native, err := QueryRows(ctx, conn, `SELECT `+v+`, ARRAY[`+v+`, NULL]`, nil, true, false)
			require.NoError(t, err, v)
			text, err := QueryRows(ctx, conn, `SELECT `+v, nil, true, true)
			require.NoError(t, err, v)
			want := text.Rows[0].([]any)[0]
			got := native.Rows[0].([]any)
			assert.Equal(t, want, got[0], "%s under %s", v, digits)
			assert.Equal(t, []any{want, nil}, got[1], "%s[] under %s", v, digits)
		}
	}

	_, err = conn.Exec(ctx, "SET extra_float_digits = 1")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	executeBatch(rec, conn, ctx, []QueryItem{
		{Query: "SET extra_float_digits = 0"},
		{Query: `SELECT point(` + x + `, 1) AS p`},
	}, true, false, txnOptions{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			Rows [][]json.RawMessage `json:"rows"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 2)
	assert.Equal(t, `"(0.3,1)"`, string(body.Results[1].Rows[0][0]))
}
