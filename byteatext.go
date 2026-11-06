package pgserverless

import (
	"github.com/jackc/pgx/v5/pgtype"
)

// pgTextByteaCodec has PostgreSQL send bytea as text, spelled in the session
// bytea_output, and keeps that text as the value. bytea_output is not reported
// in ParameterStatus, so a binary bytea always came back as hex where Neon
// returns byteaout's escape spelling under bytea_output = escape. Parameters
// still encode as pgtype.ByteaCodec does.
type pgTextByteaCodec struct {
	pgtype.ByteaCodec
}

func (pgTextByteaCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextByteaCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.ByteaCodec.DecodeValue(m, oid, format, src)
}

// pgTextByteaArrayCodec does the same for bytea[], whose literal coerceNeonValue splits.
type pgTextByteaArrayCodec struct {
	*pgtype.ArrayCodec
}

func (pgTextByteaArrayCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextByteaArrayCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.ArrayCodec.DecodeValue(m, oid, format, src)
}

// useTextBytea makes m ask for bytea and bytea[] results as text. It must run
// before the query is sent, since the result formats go out with it.
func useTextBytea(m *pgtype.Map) {
	if m == nil {
		return
	}
	if t, ok := m.TypeForOID(pgtype.ByteaOID); ok {
		if _, done := t.Codec.(pgTextByteaCodec); done {
			return
		}
	}
	bytea := &pgtype.Type{Name: "bytea", OID: pgtype.ByteaOID, Codec: pgTextByteaCodec{}}
	m.RegisterTypes([]*pgtype.Type{
		bytea,
		{Name: "_bytea", OID: pgtype.ByteaArrayOID, Codec: pgTextByteaArrayCodec{&pgtype.ArrayCodec{ElementType: bytea}}},
	})
}
