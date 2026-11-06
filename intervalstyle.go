package pgserverless

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The IntervalStyle values PostgreSQL reports; any other spelling is read as the default, postgres.
const (
	intervalStylePostgres        = "postgres"
	intervalStylePostgresVerbose = "postgres_verbose"
	intervalStyleSQLStandard     = "sql_standard"
	intervalStyleISO8601         = "iso_8601"
)

// styledInterval is an interval decoded under a session IntervalStyle other than postgres.
type styledInterval struct {
	pgtype.Interval
	style string
}

// styledIntervalCodec decodes to a styledInterval and otherwise behaves as pgtype.IntervalCodec, so parameters still encode.
type styledIntervalCodec struct {
	pgtype.IntervalCodec
	style string
}

func (c *styledIntervalCodec) DecodeValue(m *pgtype.Map, oid uint32, format int16, src []byte) (any, error) {
	v, err := c.IntervalCodec.DecodeValue(m, oid, format, src)
	iv, ok := v.(pgtype.Interval)
	if err != nil || !ok {
		return v, err
	}
	return styledInterval{Interval: iv, style: c.style}, nil
}

// useSessionIntervalStyle makes m decode interval, alone and inside arrays and
// records, so clockText spells it as interval_out does under the IntervalStyle
// PostgreSQL last reported. Like useSessionTimeZone it follows a SET in an earlier
// batch item, and m's types are replaced only when the style changes.
func useSessionIntervalStyle(m *pgtype.Map, conn *pgx.Conn) {
	if m == nil || conn == nil || conn.PgConn() == nil {
		return
	}
	style := conn.PgConn().ParameterStatus("IntervalStyle")
	switch style {
	case intervalStylePostgresVerbose, intervalStyleSQLStandard, intervalStyleISO8601:
	default:
		style = intervalStylePostgres
	}
	current := intervalStylePostgres
	if t, ok := m.TypeForOID(pgtype.IntervalOID); ok {
		if c, ok := t.Codec.(*styledIntervalCodec); ok {
			current = c.style
		}
	}
	if current == style {
		return
	}
	var codec pgtype.Codec = pgtype.IntervalCodec{}
	if style != intervalStylePostgres {
		codec = &styledIntervalCodec{style: style}
	}
	iv := &pgtype.Type{Name: "interval", OID: pgtype.IntervalOID, Codec: codec}
	m.RegisterTypes([]*pgtype.Type{
		iv,
		{Name: "_interval", OID: pgtype.IntervalArrayOID, Codec: &pgtype.ArrayCodec{ElementType: iv}},
	})
}

// intervalFields splits an interval as interval2itm does, with C's truncating division.
type intervalFields struct {
	year, mon, day, hour, min, sec, usec int64
}

func splitInterval(iv pgtype.Interval) intervalFields {
	t := iv.Microseconds
	return intervalFields{
		year: int64(iv.Months / 12),
		mon:  int64(iv.Months % 12),
		day:  int64(iv.Days),
		hour: t / usecPerHour,
		min:  t % usecPerHour / usecPerMinute,
		sec:  t % usecPerMinute / usecPerSecond,
		usec: t % usecPerSecond,
	}
}

// intervalTextIn follows interval_out under the given IntervalStyle (EncodeInterval in datetime.c).
func intervalTextIn(iv pgtype.Interval, style string) string {
	if s, ok := infiniteIntervalText(iv); ok {
		return s
	}
	switch style {
	case intervalStyleSQLStandard:
		return sqlStandardIntervalText(splitInterval(iv))
	case intervalStyleISO8601:
		return iso8601IntervalText(splitInterval(iv))
	case intervalStylePostgresVerbose:
		return verboseIntervalText(splitInterval(iv))
	}
	return intervalText(iv)
}

func sqlStandardIntervalText(f intervalFields) string {
	hasNegative := f.year < 0 || f.mon < 0 || f.day < 0 || f.hour < 0 || f.min < 0 || f.sec < 0 || f.usec < 0
	hasPositive := f.year > 0 || f.mon > 0 || f.day > 0 || f.hour > 0 || f.min > 0 || f.sec > 0 || f.usec > 0
	hasYearMonth := f.year != 0 || f.mon != 0
	hasDayTime := f.day != 0 || f.hour != 0 || f.min != 0 || f.sec != 0 || f.usec != 0
	standard := !(hasNegative && hasPositive) && !(hasYearMonth && hasDayTime)

	var b strings.Builder
	if hasNegative && standard {
		// One leading sign for the whole value, as the standard has it.
		b.WriteByte('-')
		f = intervalFields{-f.year, -f.mon, -f.day, -f.hour, -f.min, -f.sec, -f.usec}
	}
	sign := func(negative bool) {
		if negative {
			b.WriteByte('-')
		} else {
			b.WriteByte('+')
		}
	}
	switch {
	case !hasNegative && !hasPositive:
		b.WriteByte('0')
	case !standard:
		// Mixed signs or mixed year-month and day-time fields: every group carries its sign.
		sign(f.year < 0 || f.mon < 0)
		b.WriteString(strconv.FormatInt(absInt64(f.year), 10))
		b.WriteByte('-')
		b.WriteString(strconv.FormatInt(absInt64(f.mon), 10))
		b.WriteByte(' ')
		sign(f.day < 0)
		b.WriteString(strconv.FormatInt(absInt64(f.day), 10))
		b.WriteByte(' ')
		sign(f.hour < 0 || f.min < 0 || f.sec < 0 || f.usec < 0)
		writeIntervalClock(&b, f)
	case hasYearMonth:
		b.WriteString(strconv.FormatInt(f.year, 10))
		b.WriteByte('-')
		b.WriteString(strconv.FormatInt(f.mon, 10))
	case f.day != 0:
		b.WriteString(strconv.FormatInt(f.day, 10))
		b.WriteByte(' ')
		writeIntervalClock(&b, f)
	default:
		writeIntervalClock(&b, f)
	}
	return b.String()
}

// writeIntervalClock writes the unpadded hours, then two-digit minutes and seconds, each as a magnitude.
func writeIntervalClock(b *strings.Builder, f intervalFields) {
	b.WriteString(strconv.FormatInt(absInt64(f.hour), 10))
	b.WriteByte(':')
	writeTwoDigits(b, uint64(absInt64(f.min)))
	b.WriteByte(':')
	writeIntervalSeconds(b, f.sec, f.usec, true)
}

func iso8601IntervalText(f intervalFields) string {
	if f == (intervalFields{}) {
		return "PT0S"
	}
	var b strings.Builder
	b.WriteByte('P')
	part := func(v int64, unit byte) {
		if v != 0 {
			b.WriteString(strconv.FormatInt(v, 10))
			b.WriteByte(unit)
		}
	}
	part(f.year, 'Y')
	part(f.mon, 'M')
	part(f.day, 'D')
	if f.hour != 0 || f.min != 0 || f.sec != 0 || f.usec != 0 {
		b.WriteByte('T')
	}
	part(f.hour, 'H')
	part(f.min, 'M')
	if f.sec != 0 || f.usec != 0 {
		if f.sec < 0 || f.usec < 0 {
			b.WriteByte('-')
		}
		writeIntervalSeconds(&b, f.sec, f.usec, false)
		b.WriteByte('S')
	}
	return b.String()
}

// verboseIntervalText follows the postgres_verbose style: the first nonzero
// field's sign becomes a trailing "ago" and every later field is written
// relative to it.
func verboseIntervalText(f intervalFields) string {
	var b strings.Builder
	b.WriteByte('@')
	isZero, isBefore := true, false
	part := func(v int64, unit string) {
		if v == 0 {
			return
		}
		if isZero {
			isBefore = v < 0
			v = absInt64(v)
		} else if isBefore {
			v = -v
		}
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(v, 10))
		b.WriteByte(' ')
		b.WriteString(unit)
		if v != 1 {
			b.WriteByte('s')
		}
		isZero = false
	}
	part(f.year, "year")
	part(f.mon, "mon")
	part(f.day, "day")
	part(f.hour, "hour")
	part(f.min, "min")
	if f.sec != 0 || f.usec != 0 {
		b.WriteByte(' ')
		if f.sec < 0 || (f.sec == 0 && f.usec < 0) {
			if isZero {
				isBefore = true
			} else if !isBefore {
				b.WriteByte('-')
			}
		} else if isBefore {
			b.WriteByte('-')
		}
		writeIntervalSeconds(&b, f.sec, f.usec, false)
		b.WriteString(" sec")
		if absInt64(f.sec) != 1 || f.usec != 0 {
			b.WriteByte('s')
		}
		isZero = false
	}
	if isZero {
		b.WriteString(" 0")
	}
	if isBefore {
		b.WriteString(" ago")
	}
	return b.String()
}

// writeIntervalSeconds follows AppendSeconds: the magnitude, padded to two digits only when asked, and a trimmed fraction.
func writeIntervalSeconds(b *strings.Builder, sec, usec int64, pad bool) {
	if pad {
		writeTwoDigits(b, uint64(absInt64(sec)))
	} else {
		b.WriteString(strconv.FormatInt(absInt64(sec), 10))
	}
	if usec != 0 {
		digits := strconv.FormatInt(absInt64(usec)+usecPerSecond, 10)[1:]
		b.WriteByte('.')
		b.WriteString(strings.TrimRight(digits, "0"))
	}
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
