package pgserverless

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// geometryText renders a pgx-decoded geometric value as point_out and its siblings print it, or reports false.
// Coordinates go through pgFloatText: pgx prints plain digits (and Point's MarshalJSON uses %g) where float8out switches to exponent form.
func geometryText(v any) (string, bool) {
	var b strings.Builder
	switch g := v.(type) {
	case pgtype.Point:
		if !g.Valid {
			return "", false
		}
		writeGeomPoint(&b, g.P)
	case pgtype.Lseg:
		if !g.Valid {
			return "", false
		}
		b.WriteByte('[')
		writeGeomPoints(&b, g.P[:])
		b.WriteByte(']')
	case pgtype.Box:
		if !g.Valid {
			return "", false
		}
		writeGeomPoints(&b, g.P[:])
	case pgtype.Path:
		if !g.Valid {
			return "", false
		}
		open, closing := byte('['), byte(']')
		if g.Closed {
			open, closing = '(', ')'
		}
		b.WriteByte(open)
		writeGeomPoints(&b, g.P)
		b.WriteByte(closing)
	case pgtype.Polygon:
		if !g.Valid {
			return "", false
		}
		b.WriteByte('(')
		writeGeomPoints(&b, g.P)
		b.WriteByte(')')
	case pgtype.Line:
		if !g.Valid {
			return "", false
		}
		b.WriteByte('{')
		b.WriteString(pgFloatText(g.A, 64))
		b.WriteByte(',')
		b.WriteString(pgFloatText(g.B, 64))
		b.WriteByte(',')
		b.WriteString(pgFloatText(g.C, 64))
		b.WriteByte('}')
	case pgtype.Circle:
		if !g.Valid {
			return "", false
		}
		b.WriteByte('<')
		writeGeomPoint(&b, g.P)
		b.WriteByte(',')
		b.WriteString(pgFloatText(g.R, 64))
		b.WriteByte('>')
	default:
		return "", false
	}
	return b.String(), true
}

func writeGeomPoints(b *strings.Builder, ps []pgtype.Vec2) {
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		writeGeomPoint(b, p)
	}
}

func writeGeomPoint(b *strings.Builder, p pgtype.Vec2) {
	b.WriteByte('(')
	b.WriteString(pgFloatText(p.X, 64))
	b.WriteByte(',')
	b.WriteString(pgFloatText(p.Y, 64))
	b.WriteByte(')')
}
