package pgserverless

import (
	"strings"
	"sync"
	"time"
	// sessionLocation loads named zones, and a binary serving this package may run on an image with no zoneinfo.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// maxSessionLocations bounds the zone cache, since a session can SET TIME ZONE to any POSIX string.
const maxSessionLocations = 256

var (
	sessionLocationsMu sync.Mutex
	sessionLocations   = map[string]*time.Location{}
)

// useSessionTimeZone makes m decode timestamptz, alone and inside arrays, ranges
// and records, into the session's TimeZone, so timestamptzText spells it as
// timestamptz_out does. pgx keeps the latest TimeZone PostgreSQL reported, so a
// SET applies to later requests and to later items of a batch that takes one
// round trip per item; a pipelined batch hears of it only at ReadyForQuery,
// after its items are read. m's types are replaced only when the zone changes.
func useSessionTimeZone(m *pgtype.Map, conn *pgx.Conn) {
	if m == nil || conn == nil || conn.PgConn() == nil {
		return
	}
	loc := sessionLocation(conn.PgConn().ParameterStatus("TimeZone"))
	if t, ok := m.TypeForOID(pgtype.TimestamptzOID); ok {
		if c, ok := t.Codec.(*pgtype.TimestamptzCodec); ok && c.ScanLocation == loc {
			return
		}
	}
	tstz := &pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: loc}}
	tstzrange := &pgtype.Type{Name: "tstzrange", OID: pgtype.TstzrangeOID, Codec: &pgtype.RangeCodec{ElementType: tstz}}
	m.RegisterTypes([]*pgtype.Type{
		tstz,
		{Name: "_timestamptz", OID: pgtype.TimestamptzArrayOID, Codec: &pgtype.ArrayCodec{ElementType: tstz}},
		tstzrange,
		{Name: "_tstzrange", OID: pgtype.TstzrangeArrayOID, Codec: &pgtype.ArrayCodec{ElementType: tstzrange}},
		{Name: "tstzmultirange", OID: pgtype.TstzmultirangeOID, Codec: &pgtype.MultirangeCodec{ElementType: tstzrange}},
	})
}

// sessionLocation resolves a reported TimeZone, falling back to UTC for an empty name or one Go cannot load.
func sessionLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	sessionLocationsMu.Lock()
	loc, ok := sessionLocations[name]
	sessionLocationsMu.Unlock()
	if ok {
		return loc
	}
	loc, ok = posixFixedZone(name)
	if !ok {
		var err error
		loc, err = time.LoadLocation(name)
		// time.Local is the sentinel timestamptzText reads as "no session zone".
		if err != nil || loc == time.Local {
			return time.UTC
		}
	}
	sessionLocationsMu.Lock()
	if len(sessionLocations) < maxSessionLocations {
		sessionLocations[name] = loc
	}
	sessionLocationsMu.Unlock()
	return loc
}

// posixFixedZone reads a POSIX TZ string with no daylight rule, which is how
// PostgreSQL reports a numeric zone ("<+05>-05" after SET TIME ZONE 5) or one
// written as "UTC+5". POSIX counts hours west of Greenwich, so the sign flips.
func posixFixedZone(s string) (*time.Location, bool) {
	var name, rest string
	if strings.HasPrefix(s, "<") {
		end := strings.IndexByte(s, '>')
		if end < 0 {
			return nil, false
		}
		name, rest = s[1:end], s[end+1:]
	} else {
		i := 0
		for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
			i++
		}
		if i < 3 {
			return nil, false
		}
		name, rest = s[:i], s[i:]
	}
	west := 1
	switch {
	case strings.HasPrefix(rest, "+"):
		rest = rest[1:]
	case strings.HasPrefix(rest, "-"):
		west = -1
		rest = rest[1:]
	}
	parts := strings.Split(rest, ":")
	if len(parts) > 3 {
		return nil, false
	}
	secs := 0
	for i, p := range parts {
		n, ok := smallDecimal(p)
		if !ok || (i == 0 && n > 24) || (i > 0 && n > 59) {
			return nil, false
		}
		secs = secs*60 + n
	}
	for range 3 - len(parts) {
		secs *= 60
	}
	return time.FixedZone(name, -west*secs), true
}

// smallDecimal parses one or two ASCII digits.
func smallDecimal(s string) (int, bool) {
	if len(s) == 0 || len(s) > 2 {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

// timestamptzText spells a finite timestamptz as timestamptz_out does in the
// zone t carries, which is the session's once useSessionTimeZone has run. A
// time in time.Local was decoded by a map with no session zone and is spelled
// in UTC, as the module always did.
func timestamptzText(m *pgtype.Map, t time.Time) (string, bool) {
	if t.Location() == time.Local {
		t = t.UTC()
	}
	wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	s, ok := encodeText(m, oidTimestamp, wall)
	if !ok {
		return "", false
	}
	_, east := t.Zone()
	zone := utcOffset(east)
	if rest, bc := strings.CutSuffix(s, " BC"); bc {
		return rest + zone + " BC", true
	}
	return s + zone, true
}

// utcOffset writes an offset as EncodeTimezone does: hours, then minutes and seconds only when nonzero.
func utcOffset(east int) string {
	var b strings.Builder
	if east < 0 {
		b.WriteByte('-')
		east = -east
	} else {
		b.WriteByte('+')
	}
	h, m, s := east/3600, east/60%60, east%60
	writeTwoDigits(&b, uint64(h))
	if m != 0 || s != 0 {
		b.WriteByte(':')
		writeTwoDigits(&b, uint64(m))
	}
	if s != 0 {
		b.WriteByte(':')
		writeTwoDigits(&b, uint64(s))
	}
	return b.String()
}
