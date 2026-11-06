package pgserverless

import (
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The DateStyle output styles other than ISO, the default, which pgx and timestamptzText already spell.
const (
	dateStyleSQL      = "SQL"
	dateStylePostgres = "Postgres"
	dateStyleGerman   = "German"
)

// maxTZAbbrevLen is MAXTZLEN, where EncodeDateTime clips a zone abbreviation.
const maxTZAbbrevLen = 10

var (
	shortWeekdays = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	shortMonths   = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// dateStyle is a non-ISO session DateStyle: its output style and whether the day comes before the month.
type dateStyle struct {
	style string
	dmy   bool
}

// styledDateCodec is pgtype.DateCodec carrying the session DateStyle, so every renderer holding the type map can read it.
type styledDateCodec struct {
	pgtype.DateCodec
	style dateStyle
}

// parseDateStyle reads a reported DateStyle ("SQL, DMY") and reports false for ISO or anything it does not know.
func parseDateStyle(s string) (dateStyle, bool) {
	name, order, _ := strings.Cut(s, ",")
	ds := dateStyle{style: strings.TrimSpace(name), dmy: strings.TrimSpace(order) == "DMY"}
	switch ds.style {
	case dateStyleSQL, dateStylePostgres, dateStyleGerman:
		return ds, true
	}
	return dateStyle{}, false
}

// useSessionDateStyle records the DateStyle PostgreSQL last reported on m's
// date type, so date, timestamp and timestamptz, alone and inside arrays,
// ranges and records, are spelled as date_out, timestamp_out and
// timestamptz_out do in it. Like useSessionTimeZone it follows a SET in an
// earlier batch item, and m's date type is replaced only when the style changes.
func useSessionDateStyle(m *pgtype.Map, conn *pgx.Conn) {
	if m == nil || conn == nil || conn.PgConn() == nil {
		return
	}
	want, styled := parseDateStyle(conn.PgConn().ParameterStatus("DateStyle"))
	current, wasStyled := sessionDateStyle(m)
	if styled == wasStyled && current == want {
		return
	}
	var codec pgtype.Codec = pgtype.DateCodec{}
	if styled {
		codec = &styledDateCodec{style: want}
	}
	m.RegisterType(&pgtype.Type{Name: "date", OID: pgtype.DateOID, Codec: codec})
}

// sessionDateStyle reports the non-ISO DateStyle useSessionDateStyle recorded on m, if any.
func sessionDateStyle(m *pgtype.Map) (dateStyle, bool) {
	if m == nil {
		return dateStyle{}, false
	}
	t, ok := m.TypeForOID(pgtype.DateOID)
	if !ok {
		return dateStyle{}, false
	}
	c, ok := t.Codec.(*styledDateCodec)
	if !ok {
		return dateStyle{}, false
	}
	return c.style, true
}

// styledDateTimeText spells a finite date, timestamp or timestamptz in the
// session's non-ISO DateStyle, reporting false under ISO so the existing
// spelling stands. A timestamptz is spelled in the zone it was decoded into.
func styledDateTimeText(m *pgtype.Map, oid uint32, v any) (string, bool) {
	t, ok := v.(time.Time)
	if !ok {
		return "", false
	}
	ds, ok := sessionDateStyle(m)
	if !ok {
		return "", false
	}
	switch oid {
	case oidDate:
		return encodeDateOnly(t, ds), true
	case oidTimestamp:
		return encodeDateTime(t, ds, false), true
	case oidTimestamptz:
		// time.Local is the sentinel for a map with no session zone, as in timestamptzText.
		if t.Location() == time.Local {
			t = t.UTC()
		}
		return encodeDateTime(t, ds, true), true
	}
	return "", false
}

// encodeDateOnly follows EncodeDateOnly in datetime.c for the SQL, Postgres and German styles.
func encodeDateOnly(t time.Time, ds dateStyle) string {
	var b strings.Builder
	writeNumericDate(&b, t, ds)
	writeEra(&b, t)
	return b.String()
}

// encodeDateTime follows EncodeDateTime in datetime.c for the SQL, Postgres
// and German styles. These print the zone abbreviation rather than the offset.
func encodeDateTime(t time.Time, ds dateStyle, withZone bool) string {
	var b strings.Builder
	if ds.style == dateStylePostgres {
		b.WriteString(shortWeekdays[t.Weekday()])
		b.WriteByte(' ')
		if ds.dmy {
			writeTwoDigits(&b, uint64(t.Day()))
			b.WriteByte(' ')
			b.WriteString(shortMonths[t.Month()-1])
		} else {
			b.WriteString(shortMonths[t.Month()-1])
			b.WriteByte(' ')
			writeTwoDigits(&b, uint64(t.Day()))
		}
		b.WriteByte(' ')
		writeTimestampClock(&b, t)
		b.WriteByte(' ')
		writeYear(&b, t)
	} else {
		writeNumericDate(&b, t, ds)
		b.WriteByte(' ')
		writeTimestampClock(&b, t)
	}
	if withZone {
		name, east := t.Zone()
		switch {
		case name != "":
			b.WriteByte(' ')
			b.WriteString(name[:min(len(name), maxTZAbbrevLen)])
		case ds.style == dateStylePostgres:
			// EncodeDateTime spaces a numeric zone only in this style, so it still parses back.
			b.WriteByte(' ')
			b.WriteString(utcOffset(east))
		default:
			b.WriteString(utcOffset(east))
		}
	}
	writeEra(&b, t)
	return b.String()
}

// writeNumericDate writes month and day (day first under DMY or German), then the year, joined as the style joins them.
func writeNumericDate(b *strings.Builder, t time.Time, ds dateStyle) {
	sep := byte('-')
	switch ds.style {
	case dateStyleSQL:
		sep = '/'
	case dateStyleGerman:
		sep = '.'
	}
	first, second := uint64(t.Month()), uint64(t.Day())
	if ds.dmy || ds.style == dateStyleGerman {
		first, second = second, first
	}
	writeTwoDigits(b, first)
	b.WriteByte(sep)
	writeTwoDigits(b, second)
	b.WriteByte(sep)
	writeYear(b, t)
}

// writeTimestampClock writes hours, minutes and seconds with a trimmed fraction, as AppendTimestampSeconds does.
func writeTimestampClock(b *strings.Builder, t time.Time) {
	writeTwoDigits(b, uint64(t.Hour()))
	b.WriteByte(':')
	writeTwoDigits(b, uint64(t.Minute()))
	b.WriteByte(':')
	writeIntervalSeconds(b, int64(t.Second()), int64(t.Nanosecond()/1000), true)
}

// writeYear writes the year zero-padded to four digits, counting a BC year from 1 as PostgreSQL does.
func writeYear(b *strings.Builder, t time.Time) {
	year := t.Year()
	if year <= 0 {
		year = 1 - year
	}
	s := strconv.Itoa(year)
	for range 4 - len(s) {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

func writeEra(b *strings.Builder, t time.Time) {
	if t.Year() <= 0 {
		b.WriteString(" BC")
	}
}
