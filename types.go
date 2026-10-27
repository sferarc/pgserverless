package pgserverless

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// PostgreSQL type OIDs for text-mode result conversion.
const (
	oidBool        uint32 = 16
	oidInt2        uint32 = 21
	oidInt4        uint32 = 23
	oidInt8        uint32 = 20
	oidFloat4      uint32 = 700
	oidFloat8      uint32 = 701
	oidMoney       uint32 = 790
	oidNumeric     uint32 = 1700
	oidJSON        uint32 = 114
	oidJSONB       uint32 = 3802
	oidBoolArray   uint32 = 1000
	oidInt2Array   uint32 = 1005
	oidInt4Array   uint32 = 1007
	oidInt8Array   uint32 = 1016
	oidFloat4Array uint32 = 1021
	oidFloat8Array uint32 = 1022
	oidNumericArr  uint32 = 1231
	oidTextArray   uint32 = 1009
	oidVarcharArr  uint32 = 1015
	oidJSONArray   uint32 = 199
	oidJSONBArray  uint32 = 3807
	oidMoneyArray  uint32 = 791
	oidUUID        uint32 = 2950
	oidUUIDArray   uint32 = 2951
	// record is what row(...) returns. pgx decodes it to a []any, like an array,
	// which is why reshapeArrayValue cannot decide from the decoded value alone.
	oidRecord uint32 = 2249

	// Types whose pgx-decoded Go value does not JSON-marshal to PostgreSQL's
	// own text. See pgTextValue.
	oidBytea         uint32 = 17
	oidXML           uint32 = 142
	oidXMLArray      uint32 = 143
	oidByteaArray    uint32 = 1001
	oidDate          uint32 = 1082
	oidTime          uint32 = 1083
	oidDateArray     uint32 = 1182
	oidTimeArray     uint32 = 1183
	oidInterval      uint32 = 1186
	oidIntervalArray uint32 = 1187

	// The MAC address and bit string types. pgx decodes macaddr and macaddr8 to
	// net.HardwareAddr, which is a []byte, and bit and varbit to pgtype.Bits,
	// which holds one. Both marshal the bytes as base64. macaddr8[] has no pgx
	// codec, so it arrives as an array literal; see textArrayElems.
	oidMacaddr       uint32 = 829
	oidMacaddr8      uint32 = 774
	oidMacaddr8Array uint32 = 775
	oidMacaddrArray  uint32 = 1040
	oidBit           uint32 = 1560
	oidBitArray      uint32 = 1561
	oidVarbit        uint32 = 1562
	oidVarbitArray   uint32 = 1563

	// The timestamp types, which are here only for their infinite values. See
	// infinityText: a finite timestamp is left as pgx decoded it.
	oidTimestamp        uint32 = 1114
	oidTimestampArray   uint32 = 1115
	oidTimestamptz      uint32 = 1184
	oidTimestamptzArray uint32 = 1185

	// The geometric types and tsvector. pgx decodes each to a pgtype struct that
	// marshals as a JSON object of pgtype's own field names. point is absent on
	// purpose: pgtype.Point carries its own MarshalJSON and already returns
	// "(1,2)". See pgTextValue.
	oidLseg          uint32 = 601
	oidPath          uint32 = 602
	oidBox           uint32 = 603
	oidPolygon       uint32 = 604
	oidLine          uint32 = 628
	oidCircle        uint32 = 718
	oidTSVector      uint32 = 3614
	oidLsegArray     uint32 = 1018
	oidPathArray     uint32 = 1019
	oidBoxArray      uint32 = 1020
	oidPolygonArray  uint32 = 1027
	oidLineArray     uint32 = 629
	oidCircleArray   uint32 = 719
	oidTSVectorArray uint32 = 3643

	// inet, whose decode is readable but carries a netmask PostgreSQL suppresses
	// for a single host. See inetHostText. cidr (OID 650) is deliberately absent:
	// PostgreSQL always prints its netmask, so pgx's spelling already matches.
	oidInet      uint32 = 869
	oidInetArray uint32 = 1041
)

// coerceNeonValue post-processes a pgx-decoded value so the JSON it serializes
// to matches what @neondatabase/serverless returns. pgx decodes int8 to int64
// and numeric to pgtype.Numeric, which JSON-marshal as numbers — but the Neon
// driver returns int8/numeric/money as *strings* to avoid JavaScript Number
// precision loss (any |value| > 2^53 silently corrupts as a JS number). We
// preserve that contract here; other types pass through unchanged.
//
// The same mismatch applies wherever pgx's Go type does not JSON-marshal to
// PostgreSQL's own text: uuid decodes to [16]byte and would serialize as 16
// numbers, and money[] has no pgx codec at all so it arrives as one raw array
// literal string. Both are coerced back to what the driver returns, as are the
// types listed in pgTextValue.
//
// float4/float8 are the one case where the mismatch is not a shape difference
// but a hard marshal failure: NaN and the infinities are ordinary values of both
// types, and encoding/json refuses them, which fails the whole response rather
// than the single column. They are coerced to PostgreSQL's own text.
//
// Ranges and multiranges are the one family whose element type is not fixed by
// the column's OID, so they are dispatched on the decoded Go value and rendered
// by rangeTextValue rather than listed in the switch below.
//
// m is the connection's own type map, used to render a value back to
// PostgreSQL text. It may be nil, in which case those types pass through
// unchanged rather than the call failing.
func coerceNeonValue(m *pgtype.Map, v any, typeOID uint32) any {
	if v == nil {
		return nil
	}
	// Checked on the value rather than on typeOID: an infinite date, timestamp
	// and timestamptz all decode to the same pgtype.InfinityModifier, and a
	// finite value of any of them must not be touched. See infinityText. It runs
	// before the slice dispatch below because an infinity is a scalar bound: a
	// timestamp array reaches the recursion element by element and each element
	// is checked there.
	if s, ok := infinityText(v); ok {
		return s
	}
	if arr, ok := v.([]any); ok {
		elemOID, isArray := neonArrayElemOID(typeOID)
		if !isArray {
			// Only asked once the value is already slice-shaped, so a scalar
			// column never pays for the type-map lookup.
			elemOID, isArray = rangeArrayElemOID(m, typeOID)
		}
		if isArray {
			out := make([]any, len(arr))
			for i, e := range arr {
				out[i] = coerceNeonValue(m, e, elemOID)
			}
			return out
		}
	}
	if s, ok := rangeTextValue(m, v, typeOID); ok {
		return s
	}
	switch typeOID {
	case oidInt8:
		if n, ok := v.(int64); ok {
			return strconv.FormatInt(n, 10)
		}
	case oidNumeric, oidMoney:
		if s, ok := numericString(v); ok {
			return s
		}
	case oidFloat4, oidFloat8:
		if s, ok := nonFiniteFloatString(v); ok {
			return s
		}
	case oidUUID:
		if s, ok := uuidString(v); ok {
			return s
		}
	case oidMoneyArray:
		if elems, ok := textArrayElems(v, oidMoney); ok {
			return elems
		}
	case oidMacaddr8Array:
		if elems, ok := textArrayElems(v, oidMacaddr8); ok {
			return elems
		}
	case oidInet:
		if s, ok := inetHostText(v); ok {
			return s
		}
	case oidTime, oidInterval:
		if s, ok := clockText(v); ok {
			return s
		}
		if s, ok := pgTextValue(m, v, typeOID); ok {
			return s
		}
	case oidBytea, oidXML, oidDate,
		oidMacaddr, oidMacaddr8, oidBit, oidVarbit,
		oidLseg, oidPath, oidBox, oidPolygon, oidLine, oidCircle:
		if s, ok := pgTextValue(m, v, typeOID); ok {
			return s
		}
	case oidTSVector:
		if s, ok := tsvectorText(v); ok {
			return s
		}
		if s, ok := pgTextValue(m, v, typeOID); ok {
			return s
		}
	}
	return v
}

// tsvectorLexemeEscaper doubles a quote and a backslash inside a lexeme, as
// PostgreSQL's tsvectorout does. pgx's own text encode backslash-escapes the
// quote instead, so "c'd" came back as 'c\'d' where PostgreSQL doubles the quote.
var tsvectorLexemeEscaper = strings.NewReplacer(`'`, `''`, `\`, `\\`)

// tsvectorText renders a pgx-decoded tsvector in PostgreSQL's own spelling,
// reporting false for anything that is not a valid pgtype.TSVector. It needs no
// type map, so a tsvector is rendered even when the caller has none.
func tsvectorText(v any) (string, bool) {
	tsv, ok := v.(pgtype.TSVector)
	if !ok || !tsv.Valid {
		return "", false
	}
	var b strings.Builder
	for i, lex := range tsv.Lexemes {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('\'')
		b.WriteString(tsvectorLexemeEscaper.Replace(lex.Word))
		b.WriteByte('\'')
		for j, p := range lex.Positions {
			if j == 0 {
				b.WriteByte(':')
			} else {
				b.WriteByte(',')
			}
			b.WriteString(p.String())
		}
	}
	return b.String(), true
}

// inetHostText renders an inet that addresses a single host as the bare address,
// which is what PostgreSQL prints for it. pgx decodes inet to netip.Prefix, which
// marshals through MarshalText and so always carries the prefix length: the
// column holding 192.168.1.1 reached the client as "192.168.1.1/32" where
// PostgreSQL, and therefore @neondatabase/serverless, says "192.168.1.1".
//
// That is the macaddr failure mode rather than the leaked-struct one. The value
// is a short printable identifier a client reads, logs and compares, and
// "192.168.1.1/32" is a string that looks like one, so an allowlist check or a
// join against a text column silently stops matching. It is not lossy: inet
// input reads a missing prefix as the full width, so both spellings are the same
// value.
//
// It reports false for every prefix that is not a single host, because
// PostgreSQL prints those with the netmask and pgx already spells them that way.
// pgTextValue would not do this: pgx's own encode writes the /32 too.
//
// The width is taken from the address rather than assumed, so an IPv6 host is
// /128 and an IPv4-mapped IPv6 address is the /128 PostgreSQL prints it with.
func inetHostText(v any) (string, bool) {
	p, ok := v.(netip.Prefix)
	if !ok || !p.IsValid() || p.Bits() != p.Addr().BitLen() {
		return "", false
	}
	return p.Addr().String(), true
}

// pgTextValue renders a pgx-decoded value back to PostgreSQL's text format
// using the connection's own type map, which is the same machinery pgx uses to
// send a value of that type upstream. It covers the column types whose Go
// decode does not JSON-marshal to anything a client can read back:
//
//	bytea, xml     decode to []byte, which encoding/json base64-encodes, so
//	               '\x0102' reaches the client as "AQI=" with nothing saying so
//	date           decodes to time.Time and marshals as an RFC 3339 instant,
//	               "2024-01-01T00:00:00Z" for a type that carries no time
//	macaddr,       decode to net.HardwareAddr, which is a []byte with no
//	macaddr8       MarshalJSON, so 08:00:2b:01:02:03 reaches the client as
//	               "CAArAQID": bytea's failure mode on a short printable
//	               identifier a client reads and compares
//	bit, varbit    decode to pgtype.Bits, which marshals as
//	               {"Bytes":"oA==","Len":3,...}: the same base64, wrapped in a
//	               struct, with the bit string recoverable only by knowing how
//	               pgtype packs it
//	lseg, path,    decode to pgtype structs and marshal as objects of pgtype's
//	box, polygon,  field names, so an lseg arrived as
//	line, circle   {"P":[{"X":1,"Y":2},{"X":3,"Y":4}],"Valid":true} rather than
//	               as [(1,2),(3,4)]. point is not here: pgtype.Point carries its
//	               own MarshalJSON and already returns "(1,2)"
//	tsvector       decodes to pgtype.TSVector and marshals as a nest of Lexemes
//	               whose Weight is the ASCII code of the weight letter, so 'a':1
//	               arrived as {"Lexemes":[{"Word":"a","Positions":[{"Position":1,
//	               "Weight":68}]}],"Valid":true}
//
// A value that already arrived as a string (a type pgx has no codec for, such
// as timetz, or the simple protocol) is PostgreSQL text already and is returned
// as-is. An encode failure reports false so the caller leaves the value alone:
// the pre-existing shape is wrong but it is not lossy, and dropping the column
// would be.
//
// The rendering is pgx's rather than the server's. time, interval and tsvector,
// where pgx's spelling differs from PostgreSQL's, are rendered by clockText and
// tsvectorText first.
func pgTextValue(m *pgtype.Map, v any, typeOID uint32) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	if m == nil {
		return "", false
	}
	buf, err := m.Encode(typeOID, pgtype.TextFormatCode, v, nil)
	if err != nil || buf == nil {
		return "", false
	}
	return string(buf), true
}

// rangeTextValue renders a pgx-decoded range or multirange back to
// PostgreSQL's own literal, reporting false for every other value.
//
// pgx decodes every range to pgtype.Range[any] and every multirange to a
// pgtype.Multirange of those, and neither marshals to anything a client can
// read: an int4range reaches the response as
// {"Lower":1,"Upper":3,"LowerType":105,"UpperType":101,"Valid":true}, the Go
// field names plus the bound-type bytes as numbers, and a multirange as a JSON
// array of the same. @neondatabase/serverless returns the literal text ("[1,3)")
// because pg-types registers no range parser and passes the raw text through,
// so the struct is both unreadable and wrong against the contract. It can also
// be lossy on its own: an int8range bound past 2^53 is a JSON number, and
// 9007199254740993 comes back as 9007199254740992.
//
// This is not another entry in pgTextValue's list because pgtype.Map.Encode
// cannot render these values back. The range codec's encode plan needs a
// concrete element type and the decoded bounds are `any`, so it fails with
// "cannot encode … as element of range" and the column would be left alone.
// The literal is therefore assembled here from the bounds and the bound types.
//
// The element type is read off the connection's own type map rather than from a
// table of range OIDs, which is what keeps this to one code path: it covers the
// six built-in range types, the six multirange types, and any range type a
// caller registers on the map, without naming any of them.
func rangeTextValue(m *pgtype.Map, v any, typeOID uint32) (string, bool) {
	if m == nil {
		return "", false
	}
	switch val := v.(type) {
	case pgtype.Range[any]:
		elemOID, ok := rangeElemOID(m, typeOID)
		if !ok {
			return "", false
		}
		return rangeLiteral(m, val, elemOID)
	case pgtype.Multirange[pgtype.Range[any]]:
		rangeOID, ok := multirangeRangeOID(m, typeOID)
		if !ok {
			return "", false
		}
		elemOID, ok := rangeElemOID(m, rangeOID)
		if !ok {
			return "", false
		}
		var b strings.Builder
		b.WriteByte('{')
		for i, r := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			// The range literals are not quoted inside the braces, which is
			// what PostgreSQL's multirange_out writes: {[1,3),[5,7)}.
			lit, ok := rangeLiteral(m, r, elemOID)
			if !ok {
				return "", false
			}
			b.WriteString(lit)
		}
		b.WriteByte('}')
		return b.String(), true
	}
	return "", false
}

// rangeLiteral assembles one range literal: the lower bound's bracket, the two
// bounds separated by a comma, and the upper bound's bracket. An empty range is
// the word "empty" and carries no bounds at all, which is a different value from
// a range with two unbounded ends ("(,)"): the first contains nothing, the
// second contains everything.
//
// It reports false when a bound cannot be rendered, so the caller leaves the
// value exactly as pgx decoded it. A half-rendered range would be a wrong value
// rather than an ugly one.
func rangeLiteral(m *pgtype.Map, r pgtype.Range[any], elemOID uint32) (string, bool) {
	if !r.Valid {
		return "", false
	}
	if r.LowerType == pgtype.Empty || r.UpperType == pgtype.Empty {
		return "empty", true
	}
	lower, ok := rangeBoundText(m, r.Lower, r.LowerType, elemOID)
	if !ok {
		return "", false
	}
	upper, ok := rangeBoundText(m, r.Upper, r.UpperType, elemOID)
	if !ok {
		return "", false
	}

	var b strings.Builder
	b.Grow(len(lower) + len(upper) + 3)
	if r.LowerType == pgtype.Inclusive {
		b.WriteByte('[')
	} else {
		b.WriteByte('(')
	}
	b.WriteString(lower)
	b.WriteByte(',')
	b.WriteString(upper)
	if r.UpperType == pgtype.Inclusive {
		b.WriteByte(']')
	} else {
		b.WriteByte(')')
	}
	return b.String(), true
}

// rangeBoundText renders one bound as it appears inside a range literal. An
// unbounded end is written as nothing at all, which is why it returns before
// the quoting: quoting the empty string would produce `("",3)`, a range whose
// lower bound is the empty string rather than one with no lower bound.
func rangeBoundText(m *pgtype.Map, bound any, boundType pgtype.BoundType, elemOID uint32) (string, bool) {
	if boundType == pgtype.Unbounded || bound == nil {
		return "", true
	}
	s, ok := pgTextValue(m, bound, elemOID)
	if !ok {
		return "", false
	}
	if elemOID == pgtype.TimestamptzOID {
		s = utcOffsetText(s)
	}
	return quoteRangeBound(s), true
}

// utcOffsetText respells the "Z" pgx writes after a UTC timestamptz as the
// "+00" timestamptz_out prints in a UTC session, keeping a trailing " BC".
func utcOffsetText(s string) string {
	if rest, ok := strings.CutSuffix(s, "Z BC"); ok {
		return rest + "+00 BC"
	}
	if rest, ok := strings.CutSuffix(s, "Z"); ok {
		return rest + "+00"
	}
	return s
}

// rangeElemOID reports the element type OID of a range type, and
// multirangeRangeOID the range type OID of a multirange type, both read off the
// connection's type map. Each reports false for a type the map does not carry or
// whose codec is not the expected one, which is how a value that only looks like
// a range is left alone.
func rangeElemOID(m *pgtype.Map, rangeOID uint32) (uint32, bool) {
	t, ok := m.TypeForOID(rangeOID)
	if !ok {
		return 0, false
	}
	codec, ok := t.Codec.(*pgtype.RangeCodec)
	if !ok || codec.ElementType == nil {
		return 0, false
	}
	return codec.ElementType.OID, true
}

func multirangeRangeOID(m *pgtype.Map, multirangeOID uint32) (uint32, bool) {
	t, ok := m.TypeForOID(multirangeOID)
	if !ok {
		return 0, false
	}
	codec, ok := t.Codec.(*pgtype.MultirangeCodec)
	if !ok || codec.ElementType == nil {
		return 0, false
	}
	return codec.ElementType.OID, true
}

// rangeArrayElemOID reports the element OID of an array whose elements are
// ranges or multiranges, so coerceNeonValue recurses into it and each element
// reaches rangeTextValue.
//
// neonArrayElemOID's static table deliberately does not list these. There are
// twelve built-in range and multirange types before their array forms, and a
// range type registered on the connection has no fixed OID to list at all, so
// the type map answers this instead of a constant. It is consulted only for an
// array type the static table does not name, so the common columns still cost
// nothing.
func rangeArrayElemOID(m *pgtype.Map, arrayOID uint32) (uint32, bool) {
	if m == nil {
		return 0, false
	}
	t, ok := m.TypeForOID(arrayOID)
	if !ok {
		return 0, false
	}
	codec, ok := t.Codec.(*pgtype.ArrayCodec)
	if !ok || codec.ElementType == nil {
		return 0, false
	}
	switch codec.ElementType.Codec.(type) {
	case *pgtype.RangeCodec, *pgtype.MultirangeCodec:
		return codec.ElementType.OID, true
	}
	return 0, false
}

// neonArrayElemOID maps an array type OID whose elements need coercion to the
// element's own OID. It reports false for every other array type, whose
// elements pgx already decodes into the shape the driver returns. money[] is
// deliberately absent: pgx hands it back as one string rather than a slice, so
// it is handled by textArrayElems instead. macaddr8[] is absent for the same
// reason: pgx registers no codec for OID 775 either.
//
// The float arrays are here for the marshal-failure reason rather than the shape
// reason: a single NaN or infinity anywhere in a float4[]/float8[] would fail the
// whole response. Their finite elements pass through coerceNeonValue unchanged
// and stay JSON numbers.
//
// The timestamp arrays are here for a third reason: only their infinite elements
// need anything, and those are coerced on the decoded value rather than on this
// OID (see infinityText). Their finite elements pass through unchanged and stay
// RFC 3339 instants. timestamp[] and timestamptz[] are listed only so the walk
// reaches the elements at all; date[] was already reached, above.
//
// inet[] is a fourth reason: only its single-host elements need anything, and a
// element carrying a real netmask passes through unchanged (see inetHostText).
// cidr[] is absent because cidr needs nothing. point[] is absent for the same
// reason, and box[] is present because pgx decodes it from the binary format the
// JSON path negotiates, where PostgreSQL's semicolon array delimiter never
// arises.
func neonArrayElemOID(arrayOID uint32) (uint32, bool) {
	switch arrayOID {
	case oidInt8Array:
		return oidInt8, true
	case oidNumericArr:
		return oidNumeric, true
	case oidUUIDArray:
		return oidUUID, true
	case oidByteaArray:
		return oidBytea, true
	case oidXMLArray:
		return oidXML, true
	case oidDateArray:
		return oidDate, true
	case oidTimeArray:
		return oidTime, true
	case oidIntervalArray:
		return oidInterval, true
	case oidMacaddrArray:
		return oidMacaddr, true
	case oidBitArray:
		return oidBit, true
	case oidVarbitArray:
		return oidVarbit, true
	case oidFloat4Array:
		return oidFloat4, true
	case oidFloat8Array:
		return oidFloat8, true
	case oidTimestampArray:
		return oidTimestamp, true
	case oidTimestamptzArray:
		return oidTimestamptz, true
	case oidLsegArray:
		return oidLseg, true
	case oidPathArray:
		return oidPath, true
	case oidBoxArray:
		return oidBox, true
	case oidPolygonArray:
		return oidPolygon, true
	case oidLineArray:
		return oidLine, true
	case oidCircleArray:
		return oidCircle, true
	case oidTSVectorArray:
		return oidTSVector, true
	case oidInetArray:
		return oidInet, true
	}
	return 0, false
}

// pgBinaryFormat is PostgreSQL's binary result format code. The extended
// protocol pgx uses by default asks for binary results for every type it has a
// codec for, which is every array type reshapeArrayValue can act on.
const pgBinaryFormat int16 = 1

// pgMaxArrayDims is PostgreSQL's MAXDIM, the hard limit on an array's
// dimensionality. A header claiming more than this did not come from
// PostgreSQL, so it is not reshaped.
const pgMaxArrayDims = 6

// reshapeArrayValue restores the dimensions of a multi-dimensional array column,
// which pgx's decode drops.
//
// `rows.Values()` decodes every array into a flat []any regardless of
// dimensionality: `select array[array[1,2],array[3,4]]` comes back as
// [1,2,3,4], and nothing in the response says a dimension was lost. The Neon
// driver returns nested JS arrays there (pg-types parses the array literal
// dimension by dimension), so the flat shape is both wrong against the contract
// and silently wrong against the data: a client indexing rows[0].a[1] reads
// element 2 where PostgreSQL holds [3,4]. The parameter path already accepts
// `{{1,2},{3,4}}`, so before this a nested array could be written and could not
// be read back in the shape it was written.
//
// The dimensions are read from the value's own binary array header rather than
// re-derived, so the reshape follows what the server actually sent.
//
// A []any is not proof that the column is an array: pgtype.RecordCodec decodes
// `row(...)` to a []any too, and a record's binary layout (int32 field count,
// then a type OID, a length and the data per field) is read by an array header
// parser as ndim, flags, element OID and a dimension list. A two-field record
// whose first field carries twelve or more bytes of user data therefore has its
// dimensions read straight out of that data, and `row('...'::bytea, 7)` was
// re-nested to [[bytea],[7]] whenever those bytes happened to account for two
// fields. So the column's registered codec decides, and only a pgtype.ArrayCodec
// is reshaped. Everything else is returned untouched: scalars, records,
// one-dimensional arrays (already correct), the empty array, and the text-format
// arrays pgx has no codec for (money[] and macaddr8[], which textArrayElems
// already nests from the literal's own braces).
//
// The element OID the codec declares is checked against the one in the header as
// a second, independent agreement that these bytes are this column's array. When
// anything disagrees the value is left exactly as pgx decoded it, which is the
// old flat shape rather than a wrong nesting.
//
// It runs after coerceNeonValue, not before: the coercion walks a flat []any
// against the element OID, and nesting first would hide every element from it,
// so an int8[][] would lose its string contract to gain its shape.
func reshapeArrayValue(m *pgtype.Map, v any, raw []byte, typeOID uint32, format int16) any {
	if format != pgBinaryFormat || m == nil {
		return v
	}
	flat, ok := v.([]any)
	if !ok {
		return v
	}
	t, ok := m.TypeForOID(typeOID)
	if !ok {
		return v
	}
	codec, ok := t.Codec.(*pgtype.ArrayCodec)
	if !ok || codec.ElementType == nil {
		return v
	}
	dims, elemOID := binaryArrayDims(raw)
	if len(dims) < 2 || elemOID != codec.ElementType.OID {
		return v
	}
	nested, ok := nestFlatArray(flat, dims)
	if !ok {
		return v
	}
	return nested
}

// binaryArrayDims reads the per-dimension lengths and the element type OID from
// a binary array value's header, which is int32 ndim, int32 flags, uint32
// element OID, then int32 length and int32 lower bound per dimension. It reports
// nil dimensions for a value that is not a multi-dimensional array, including an
// empty array (ndim 0) and a truncated or implausible header.
//
// It does not and cannot tell an array from anything else that starts with a
// small integer: the caller decides that from the column's type. See
// reshapeArrayValue.
func binaryArrayDims(raw []byte) ([]int, uint32) {
	const headerLen = 12
	const dimLen = 8
	if len(raw) < headerLen {
		return nil, 0
	}
	ndim := int(int32(binary.BigEndian.Uint32(raw)))
	if ndim < 2 || ndim > pgMaxArrayDims {
		return nil, 0
	}
	if len(raw) < headerLen+ndim*dimLen {
		return nil, 0
	}
	elemOID := binary.BigEndian.Uint32(raw[8:])
	dims := make([]int, ndim)
	for i := range dims {
		n := int(int32(binary.BigEndian.Uint32(raw[headerLen+i*dimLen:])))
		if n < 0 {
			return nil, 0
		}
		dims[i] = n
	}
	return dims, elemOID
}

// nestFlatArray rebuilds dims-shaped nested slices from the row-major flat
// element list pgx decoded. It reports false unless the dimensions account for
// exactly the elements present, so a header that disagrees with the decoded
// value leaves that value alone rather than reshaping it into a lie.
//
// The running product is compared by division rather than multiplied out, so a
// header claiming dimensions whose product overflows cannot wrap around into a
// length that happens to match.
func nestFlatArray(flat []any, dims []int) ([]any, bool) {
	total := 1
	for _, d := range dims {
		if d <= 0 || total > len(flat)/d {
			return nil, false
		}
		total *= d
	}
	if total != len(flat) {
		return nil, false
	}

	var build func(level, offset int) []any
	build = func(level, offset int) []any {
		out := make([]any, dims[level])
		if level == len(dims)-1 {
			copy(out, flat[offset:offset+dims[level]])
			return out
		}
		stride := 1
		for _, d := range dims[level+1:] {
			stride *= d
		}
		for i := range out {
			out[i] = build(level+1, offset+i*stride)
		}
		return out
	}
	return build(0, 0), true
}

// nonFiniteFloatString renders a float that encoding/json cannot marshal as the
// text PostgreSQL prints for it, reporting false for every finite value so those
// stay JSON numbers. NaN, Infinity and -Infinity are ordinary float4 and float8
// values that any customer table can hold, but encoding/json rejects them
// outright, so an uncoerced one turns the entire query response into a 500 and
// takes every other row and column with it.
//
// The spellings are PostgreSQL's own text output, which is what the endpoint's
// raw-text mode already returns for these values and what formatPgArrayFloat
// writes on the request side, so a value sent through this endpoint reads back
// as the same value.
func nonFiniteFloatString(v any) (string, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	default:
		return "", false
	}
	switch {
	case math.IsNaN(f):
		return "NaN", true
	case math.IsInf(f, 1):
		return "Infinity", true
	case math.IsInf(f, -1):
		return "-Infinity", true
	}
	return "", false
}

// infinityText renders an infinite date or timestamp as the text PostgreSQL
// prints for it, reporting false for every other value so those are left alone.
//
// infinity and -infinity are ordinary values of date, timestamp and timestamptz.
// There is no time.Time for either, so pgx decodes both to
// pgtype.InfinityModifier, an int8 enum whose values are 1 and -1. encoding/json
// marshals it as those numbers, so a timestamp column holding infinity reached
// the client as 1 with nothing marking it as anything else. That is worse than
// the leaked-struct shapes pgTextValue fixes: a struct at least looks foreign,
// while 1 is indistinguishable from a value the column could hold.
//
// It matches on the decoded Go value rather than on the column's type OID,
// because all three types decode an infinite value to this one type, and because
// keying on the OID would mean coercing finite timestamps too. Those stay the
// RFC 3339 instants this endpoint has always returned, as a deliberate
// divergence from PostgreSQL's text form.
//
// The spellings are PostgreSQL's own and are what raw-text mode already returns
// for these values, so a row reads back the same either way.
func infinityText(v any) (string, bool) {
	im, ok := v.(pgtype.InfinityModifier)
	if !ok || im == pgtype.Finite {
		return "", false
	}
	return im.String(), true
}

// uuidString renders a pgx-decoded uuid as the canonical dashed text
// PostgreSQL prints. pgx decodes uuid to [16]byte, and encoding/json renders a
// byte array as 16 JSON numbers rather than as a string, so an uncoerced uuid
// primary key reaches the client as [160,238,...]. A uuid that already arrived
// as a string (simple protocol) needs no coercion and is left alone.
func uuidString(v any) (string, bool) {
	b, ok := v.([16]byte)
	if !ok {
		return "", false
	}
	val, err := pgtype.UUID{Bytes: b, Valid: true}.Value()
	if err != nil {
		return "", false
	}
	s, ok := val.(string)
	return s, ok
}

// textArrayElems splits an array column pgx has no codec for (money[], OID 791,
// and macaddr8[], OID 775) into its elements. Such a column arrives as the raw
// array literal string ("{$1.23,$2.34}") rather than as a slice, and the whole
// array would otherwise serialize as one JSON string. Elements keep
// PostgreSQL's own text, which is what the driver returns for the scalar type.
// money carries group separators that make splitting on commas wrong:
// PostgreSQL quotes such an element, and parsePgArray honors the quoting and
// nests a multi-dimensional literal.
func textArrayElems(v any, elemOID uint32) ([]any, bool) {
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	elems, err := parsePgArray([]byte(s), elemOID)
	if err != nil {
		return nil, false
	}
	return elems, true
}

// numericString renders a pgx-decoded numeric/money value as its canonical
// decimal string, matching the Neon driver's string representation.
func numericString(v any) (string, bool) {
	if n, ok := v.(pgtype.Numeric); ok {
		val, err := n.Value() // driver.Value: string for a valid numeric
		if err == nil {
			if s, ok := val.(string); ok {
				return s, true
			}
		}
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

// convertTextValue converts a text-format PG value to its native Go type
// based on the column's type OID. Returns the raw string for unknown types.
func convertTextValue(raw []byte, typeOID uint32) any {
	if raw == nil {
		return nil
	}
	s := string(raw)
	switch typeOID {
	case oidBool:
		return s == "t" || s == "true" || s == "TRUE"
	case oidInt2, oidInt4:
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
		return s
	case oidInt8:
		// Return as string to avoid JS precision loss for int8
		return s
	case oidFloat4, oidFloat8:
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
		return s
	case oidNumeric:
		// Return numeric as string to preserve precision
		return s
	case oidJSON, oidJSONB:
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			return v
		}
		return s
	case oidBoolArray, oidInt2Array, oidInt4Array, oidInt8Array,
		oidFloat4Array, oidFloat8Array, oidTextArray, oidVarcharArr,
		oidJSONArray, oidJSONBArray:
		elemOID := arrayElemOID(typeOID)
		arr, err := parsePgArray(raw, elemOID)
		if err != nil {
			return s
		}
		return arr
	default:
		return s
	}
}

// arrayElemOID maps an array type OID to its element type OID.
func arrayElemOID(arrOID uint32) uint32 {
	switch arrOID {
	case oidBoolArray:
		return oidBool
	case oidInt2Array:
		return oidInt2
	case oidInt4Array:
		return oidInt4
	case oidInt8Array:
		return oidInt8
	case oidFloat4Array:
		return oidFloat4
	case oidFloat8Array:
		return oidFloat8
	case oidTextArray, oidVarcharArr:
		return 25 // oid for text
	case oidJSONArray:
		return oidJSON
	case oidJSONBArray:
		return oidJSONB
	default:
		return 25
	}
}

// parsePgArray parses a PostgreSQL text-format array literal like
// {1,2,3} or {"hello","world"} into a Go slice. A multi-dimensional literal
// such as {{1,2},{3,4}} becomes nested slices, and a leading dimension
// decoration ("[0:1]={1,2}") is skipped.
func parsePgArray(raw []byte, elemTypeOID uint32) ([]any, error) {
	s := string(raw)
	if strings.HasPrefix(s, "[") {
		if i := strings.IndexByte(s, '='); i >= 0 {
			s = s[i+1:]
		}
	}
	p := pgArrayParser{s: s, elemOID: elemTypeOID}
	arr, err := p.parseArray()
	if err != nil {
		return nil, err
	}
	if p.pos != len(s) {
		return nil, fmt.Errorf("invalid array literal: %q", s)
	}
	return arr, nil
}

type pgArrayParser struct {
	s       string
	pos     int
	elemOID uint32
}

func (p *pgArrayParser) parseArray() ([]any, error) {
	if p.pos >= len(p.s) || p.s[p.pos] != '{' {
		return nil, fmt.Errorf("invalid array literal: %q", p.s)
	}
	p.pos++
	result := []any{}
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return result, nil
	}
	for {
		if p.pos >= len(p.s) {
			return nil, fmt.Errorf("invalid array literal: %q", p.s)
		}
		if p.s[p.pos] == '{' {
			sub, err := p.parseArray()
			if err != nil {
				return nil, err
			}
			result = append(result, sub)
		} else {
			elem, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			result = append(result, elem)
		}
		if p.pos >= len(p.s) {
			return nil, fmt.Errorf("invalid array literal: %q", p.s)
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return result, nil
		default:
			return nil, fmt.Errorf("invalid array literal: %q", p.s)
		}
	}
}

// parseElement reads one element up to, not including, the delimiter or
// closing brace that ends it.
func (p *pgArrayParser) parseElement() (any, error) {
	var current strings.Builder
	inQuote := false
	wasQuoted := false
	for ; p.pos < len(p.s); p.pos++ {
		ch := p.s[p.pos]
		switch {
		case ch == '\\':
			p.pos++
			if p.pos >= len(p.s) {
				return nil, fmt.Errorf("invalid array literal: %q", p.s)
			}
			current.WriteByte(p.s[p.pos])
		case ch == '"':
			wasQuoted = true
			inQuote = !inQuote
		case !inQuote && (ch == ',' || ch == '}'):
			return convertArrayElem(current.String(), p.elemOID, wasQuoted), nil
		case !inQuote && ch == '{':
			return nil, fmt.Errorf("invalid array literal: %q", p.s)
		default:
			current.WriteByte(ch)
		}
	}
	return nil, fmt.Errorf("invalid array literal: %q", p.s)
}

// convertArrayElem converts a single array element from PG text format.
// Unquoted NULL is SQL NULL; quoted "NULL" is the string "NULL".
func convertArrayElem(s string, elemTypeOID uint32, wasQuoted bool) any {
	if !wasQuoted && s == "NULL" {
		return nil
	}
	return convertTextValue([]byte(s), elemTypeOID)
}

// ConvertParams converts query parameters for pgx. Slices are formatted
// as PG array literals, floats as number text, maps/structs are JSON-encoded.
func ConvertParams(params []any) []any {
	if len(params) == 0 {
		return params
	}
	out := make([]any, len(params))
	for i, p := range params {
		switch v := p.(type) {
		case []any:
			out[i] = formatPgArray(v)
		case float64:
			// pgx truncates a float64 bound to an integer parameter (1.5 into int4 is 1); as text PostgreSQL rejects it with 22P02.
			out[i] = formatPgFloat(v, 64)
		case float32:
			out[i] = formatPgFloat(float64(v), 32)
		case map[string]any:
			b, err := json.Marshal(v)
			if err != nil {
				out[i] = p
			} else {
				out[i] = string(b)
			}
		default:
			out[i] = p
		}
	}
	return out
}

// formatPgArray formats a Go slice as a PG array literal: {elem1,elem2,...}
func formatPgArray(arr []any) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, elem := range arr {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(formatPgArrayElem(elem))
	}
	b.WriteByte('}')
	return b.String()
}

// formatPgArrayElem renders one element of a PG array literal. Elements reach
// this function as encoding/json decoded the request body, so a JSON number is
// always a float64 and a JSON array is always a []any, never the Go int or
// string a hand-written caller might pass. Rendering those with %v produces
// text PostgreSQL reads back as something other than what the caller sent:
// float64(1000000) becomes "1e+06", which int[] and int8[] reject with SQLSTATE
// 22P02, and a nested []any becomes Go's "[1 2]".
func formatPgArrayElem(elem any) string {
	switch v := elem.(type) {
	case nil:
		return "NULL"
	case []any:
		// A nested array is an unquoted array literal in its own right,
		// as in {{1,2},{3,4}}.
		return formatPgArray(v)
	case float64:
		return formatPgArrayFloat(v)
	case float32:
		return formatPgArrayFloat(float64(v))
	case map[string]any:
		if b, err := json.Marshal(v); err == nil {
			return quotePgArrayElem(string(b))
		}
	}
	return quotePgArrayElem(fmt.Sprintf("%v", elem))
}

// formatPgArrayFloat renders a number in a form every PostgreSQL numeric type
// accepts. Format 'f' never produces an exponent, so a whole number stays
// spelled out and an integer array type can still parse it; float8 and numeric
// read the same value either way. The non-finite values cannot arrive over JSON
// but can from a Go caller, and their bare Go spellings ("+Inf") are not ones
// PostgreSQL accepts.
func formatPgArrayFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return `"` + formatPgFloat(f, 64) + `"`
	}
	return formatPgFloat(f, 64)
}

// formatPgFloat renders f as PostgreSQL number text, spelling the non-finite values as PostgreSQL does.
// bitSize 32 keeps a widened float32 at its shortest float32 spelling, so float32(0.1) is "0.1".
func formatPgFloat(f float64, bitSize int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'f', -1, bitSize)
}

// pgArrayQuoteChars are the characters that force an element to be quoted: the
// array literal's own structure characters, the escape character, and
// whitespace, which PostgreSQL trims from an unquoted element.
const pgArrayQuoteChars = "{},\"\\ \t\n\r\v\f"

// quotePgArrayElem renders s as an array element, quoting it whenever
// PostgreSQL's array parser would otherwise read the bare text back as a
// different value. The NULL keyword is matched case-insensitively because
// PostgreSQL matches it that way, so an unquoted "null" would arrive as SQL
// NULL rather than as the three-letter string the caller sent.
func quotePgArrayElem(s string) string {
	if s != "" && !strings.EqualFold(s, "NULL") && !strings.ContainsAny(s, pgArrayQuoteChars) {
		return s
	}
	return quotePgLiteralPart(s)
}

// pgRangeQuoteChars are the characters that force a range bound to be quoted:
// the range literal's own structure characters, the escape character, and
// whitespace, which PostgreSQL trims from an unquoted bound. It differs from
// pgArrayQuoteChars because the two literals are punctuated differently, and
// there is no NULL keyword inside a range: an absent bound is written as
// nothing, which quoteRangeBound's caller handles before it gets here.
const pgRangeQuoteChars = "[](),\"\\ \t\n\r\v\f"

// quoteRangeBound renders s as a range bound, quoting it whenever PostgreSQL's
// range parser would otherwise read the bare text back as a different value.
// The empty string is always quoted, because unquoted it means an unbounded end.
func quoteRangeBound(s string) string {
	if s != "" && !strings.ContainsAny(s, pgRangeQuoteChars) {
		return s
	}
	return quotePgLiteralPart(s)
}

// quotePgLiteralPart double-quotes s and backslash-escapes the quote and the
// backslash, which is the escaping both array elements and range bounds use.
// Only the decision to quote differs between them.
func quotePgLiteralPart(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := range len(s) {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// parseCommandTag extracts the command name and row count from a PG command tag.
// Examples: "SELECT 5" -> ("SELECT", ptr(5)), "INSERT 0 1" -> ("INSERT", ptr(1))
func parseCommandTag(tag string) (string, *int) {
	parts := strings.Fields(tag)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := parts[0]
	// SELECT N, UPDATE N, DELETE N
	if len(parts) == 2 {
		if n, err := strconv.Atoi(parts[1]); err == nil {
			return cmd, &n
		}
	}
	// INSERT 0 N
	if len(parts) == 3 && cmd == "INSERT" {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			return cmd, &n
		}
	}
	return cmd, nil
}
