package pgserverless

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgTextGeomCodec has PostgreSQL send a geometric type as text and keeps that
// text as the value. point_out and its siblings spell each coordinate in the
// session extra_float_digits, which is not reported in ParameterStatus, so a
// binary point came back as (0.30000000000000004,1) where Neon returns (0.3,1)
// under extra_float_digits = 0. Parameters still encode as the embedded codec does.
type pgTextGeomCodec struct {
	pgtype.Codec
}

func (pgTextGeomCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextGeomCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.Codec.DecodeValue(m, oid, format, src)
}

// pgTextGeomArrayCodec does the same for the geometric arrays, whose literal textGeomArrayElems splits.
type pgTextGeomArrayCodec struct {
	*pgtype.ArrayCodec
}

func (pgTextGeomArrayCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextGeomArrayCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.ArrayCodec.DecodeValue(m, oid, format, src)
}

// useTextGeom makes m ask for the geometric types and their arrays as text. It
// must run before the query is sent, since the result formats go out with it.
func useTextGeom(m *pgtype.Map) {
	if m == nil {
		return
	}
	if t, ok := m.TypeForOID(pgtype.PointOID); ok {
		if _, done := t.Codec.(pgTextGeomCodec); done {
			return
		}
	}
	for _, g := range []struct {
		name          string
		oid, arrayOID uint32
		codec         pgtype.Codec
	}{
		{"point", pgtype.PointOID, pgtype.PointArrayOID, pgtype.PointCodec{}},
		{"lseg", pgtype.LsegOID, pgtype.LsegArrayOID, pgtype.LsegCodec{}},
		{"path", pgtype.PathOID, pgtype.PathArrayOID, pgtype.PathCodec{}},
		{"box", pgtype.BoxOID, pgtype.BoxArrayOID, pgtype.BoxCodec{}},
		{"polygon", pgtype.PolygonOID, pgtype.PolygonArrayOID, pgtype.PolygonCodec{}},
		{"line", pgtype.LineOID, pgtype.LineArrayOID, pgtype.LineCodec{}},
		{"circle", pgtype.CircleOID, pgtype.CircleArrayOID, pgtype.CircleCodec{}},
	} {
		elem := &pgtype.Type{Name: g.name, OID: g.oid, Codec: pgTextGeomCodec{g.codec}}
		m.RegisterTypes([]*pgtype.Type{
			elem,
			{Name: "_" + g.name, OID: g.arrayOID, Codec: pgTextGeomArrayCodec{&pgtype.ArrayCodec{ElementType: elem, Delimiter: geomArrayDelim(g.arrayOID)}}},
		})
	}
}

// geomArrayDelim is the array delimiter PostgreSQL prints for a geometric array: box's typdelim is a semicolon.
func geomArrayDelim(arrayOID uint32) byte {
	if arrayOID == oidBoxArray {
		return ';'
	}
	return ','
}

// textGeomArrayElems splits a geometric array literal into its elements, each
// kept as PostgreSQL printed it, or reports false.
func textGeomArrayElems(v any, typeOID uint32) ([]any, bool) {
	switch typeOID {
	case oidPointArray, oidLsegArray, oidPathArray, oidBoxArray, oidPolygonArray, oidLineArray, oidCircleArray:
	default:
		return nil, false
	}
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	elemOID, _ := neonArrayElemOID(typeOID)
	elems, err := parsePgArrayDelim([]byte(s), elemOID, geomArrayDelim(typeOID))
	if err != nil {
		return nil, false
	}
	return elems, true
}

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
