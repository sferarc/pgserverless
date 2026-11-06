package pgserverless

import (
	"github.com/jackc/pgx/v5/pgtype"
)

// oidRecordArray is record[], what array[row(...), ...] returns.
const oidRecordArray uint32 = 2287

// pgTextRecordCodec has PostgreSQL send an anonymous record as record_out's
// text and keeps that text as the value, as Neon returns it. Rebuilding the
// text from the binary fields failed on every field type pgx has no codec for
// (money, timetz, pg_lsn, an enum), which came back as a JSON array of base64,
// and spelled bytea and float fields without the session settings.
type pgTextRecordCodec struct {
	pgtype.RecordCodec
}

func (pgTextRecordCodec) FormatSupported(format int16) bool {
	return format == pgtype.TextFormatCode || format == pgtype.BinaryFormatCode
}

func (pgTextRecordCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextRecordCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.RecordCodec.DecodeValue(m, oid, format, src)
}

// pgTextRecordArrayCodec does the same for record[], whose literal coerceNeonValue splits.
type pgTextRecordArrayCodec struct {
	*pgtype.ArrayCodec
}

func (pgTextRecordArrayCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (c pgTextRecordArrayCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src != nil && format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return c.ArrayCodec.DecodeValue(m, oid, format, src)
}

// useTextRecord makes m ask for record and record[] results as text. It must
// run before the query is sent, since the result formats go out with it.
func useTextRecord(m *pgtype.Map) {
	if m == nil {
		return
	}
	if t, ok := m.TypeForOID(pgtype.RecordOID); ok {
		if _, done := t.Codec.(pgTextRecordCodec); done {
			return
		}
	}
	record := &pgtype.Type{Name: "record", OID: pgtype.RecordOID, Codec: pgTextRecordCodec{}}
	m.RegisterTypes([]*pgtype.Type{
		record,
		{Name: "_record", OID: pgtype.RecordArrayOID, Codec: pgTextRecordArrayCodec{&pgtype.ArrayCodec{ElementType: record}}},
	})
}
