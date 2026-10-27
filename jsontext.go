package pgserverless

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// pgx's json decode rounds numbers to float64, sorts keys and fails the row on '1e400'::jsonb; these keep PostgreSQL's text.
var (
	verbatimJSONType = &pgtype.Type{
		Name: "json", OID: oidJSON,
		Codec: &pgtype.JSONCodec{Marshal: json.Marshal, Unmarshal: unmarshalVerbatimJSON},
	}
	verbatimJSONBType = &pgtype.Type{
		Name: "jsonb", OID: oidJSONB,
		Codec: &pgtype.JSONBCodec{Marshal: json.Marshal, Unmarshal: unmarshalVerbatimJSON},
	}
	verbatimJSONArrayCodec  = &pgtype.ArrayCodec{ElementType: verbatimJSONType}
	verbatimJSONBArrayCodec = &pgtype.ArrayCodec{ElementType: verbatimJSONBType}
)

// unmarshalVerbatimJSON stores data into an *any as a json.RawMessage.
func unmarshalVerbatimJSON(data []byte, v any) error {
	dst, ok := v.(*any)
	if !ok {
		return json.Unmarshal(data, v)
	}
	if !json.Valid(data) {
		return errors.New("pgserverless: server sent invalid json")
	}
	*dst = json.RawMessage(bytes.Clone(data))
	return nil
}

func isJSONColumn(oid uint32) bool {
	switch oid {
	case oidJSON, oidJSONB, oidJSONArray, oidJSONBArray:
		return true
	}
	return false
}

// nativeRowValues is rows.Values with json, jsonb and their arrays decoded to json.RawMessage.
func nativeRowValues(rows pgx.Rows, descs []pgconn.FieldDescription, m *pgtype.Map) ([]any, error) {
	hasJSON := false
	for _, d := range descs {
		hasJSON = hasJSON || isJSONColumn(d.DataTypeOID)
	}
	if !hasJSON || m == nil {
		return rows.Values()
	}
	raw := rows.RawValues()
	if len(raw) != len(descs) {
		return nil, fmt.Errorf("pgserverless: row has %d values for %d columns", len(raw), len(descs))
	}
	values := make([]any, len(descs))
	for i, d := range descs {
		v, err := decodeNativeValue(m, d.DataTypeOID, d.Format, raw[i])
		if err != nil {
			return nil, fmt.Errorf("pgserverless: decode column %q: %w", d.Name, err)
		}
		values[i] = v
	}
	return values, nil
}

// decodeNativeValue decodes one column as rows.Values does, but with the verbatim json codecs.
func decodeNativeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	if src == nil {
		return nil, nil
	}
	switch oid {
	case oidJSON:
		return verbatimJSONType.Codec.DecodeValue(m, oid, format, src)
	case oidJSONB:
		return verbatimJSONBType.Codec.DecodeValue(m, oid, format, src)
	case oidJSONArray:
		return decodeVerbatimJSONArray(m, verbatimJSONArrayCodec, oid, format, src)
	case oidJSONBArray:
		return decodeVerbatimJSONArray(m, verbatimJSONBArrayCodec, oid, format, src)
	}
	if t, ok := m.TypeForOID(oid); ok {
		return t.Codec.DecodeValue(m, oid, format, src)
	}
	if format == pgtype.TextFormatCode {
		return string(src), nil
	}
	return bytes.Clone(src), nil
}

// decodeVerbatimJSONArray plans on c, since ArrayCodec.DecodeValue would use m's json codec; the result is flat.
func decodeVerbatimJSONArray(m *pgtype.Map, c *pgtype.ArrayCodec, oid uint32, format int16, src []byte) (any, error) {
	var arr pgtype.FlatArray[any]
	plan := c.PlanScan(m, oid, format, &arr)
	if plan == nil {
		return nil, fmt.Errorf("no scan plan for array type %d", oid)
	}
	if err := plan.Scan(src, &arr); err != nil {
		return nil, err
	}
	return []any(arr), nil
}
