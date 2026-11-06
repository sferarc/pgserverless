package pgserverless

import (
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// SELECT row(point(1e20,1)) prints ("(1e+20,1)"); the record path renders a point field through coerceNeonValue.
func TestRecordTextPointField(t *testing.T) {
	m := pgtype.NewMap()
	got, ok := recordText(m, binaryRecord(encodeBinary(t, m, oidPoint, pgtype.Point{P: pgtype.Vec2{X: 1e20, Y: 1}, Valid: true})))
	require.True(t, ok)
	assert.Equal(t, `("(1e+20,1)")`, got)
}
