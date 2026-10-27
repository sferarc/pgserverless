package pgserverless

import (
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	usecPerSecond = 1_000_000
	usecPerMinute = 60 * usecPerSecond
	usecPerHour   = 60 * usecPerMinute
)

// clockText renders a pgx-decoded time or interval in PostgreSQL's own output
// spelling, reporting false for any other value so the caller can fall back.
//
// pgx's text encode spells both differently from the server: time always
// carries six fractional digits ("12:34:56.000000" where time_out prints
// "12:34:56"), and interval always carries a time part ("1 day 00:00:00" where
// interval_out prints "1 day"). Both parse back to the same value, but a client
// comparing the native response with raw-text mode, or with a value it wrote,
// sees two strings. The interval follows the default IntervalStyle, postgres,
// because the binary value the JSON path decodes carries no session style.
func clockText(v any) (string, bool) {
	switch val := v.(type) {
	case pgtype.Time:
		if !val.Valid {
			return "", false
		}
		return timeText(val.Microseconds), true
	case pgtype.Interval:
		if !val.Valid {
			return "", false
		}
		return intervalText(val), true
	}
	return "", false
}

// timeText follows time_out: hours, minutes and seconds always two digits, and
// the fraction only when nonzero, with its trailing zeros trimmed.
func timeText(usec int64) string {
	var b strings.Builder
	writeClock(&b, uint64(usec))
	return b.String()
}

// intervalText follows EncodeInterval's postgres style, including its sign
// quirk: a field after a negative one carries an explicit "+" when positive.
func intervalText(iv pgtype.Interval) string {
	switch {
	case iv.Months == math.MaxInt32 && iv.Days == math.MaxInt32 && iv.Microseconds == math.MaxInt64:
		return "infinity"
	case iv.Months == math.MinInt32 && iv.Days == math.MinInt32 && iv.Microseconds == math.MinInt64:
		return "-infinity"
	}

	var b strings.Builder
	isZero, isBefore := true, false
	writePart := func(value int64, unit string) {
		if value == 0 {
			return
		}
		if !isZero {
			b.WriteByte(' ')
		}
		if isBefore && value > 0 {
			b.WriteByte('+')
		}
		b.WriteString(strconv.FormatInt(value, 10))
		b.WriteByte(' ')
		b.WriteString(unit)
		if value != 1 {
			b.WriteByte('s')
		}
		isBefore = value < 0
		isZero = false
	}
	writePart(int64(iv.Months/12), "year")
	writePart(int64(iv.Months%12), "mon")
	writePart(int64(iv.Days), "day")

	if isZero || iv.Microseconds != 0 {
		if !isZero {
			b.WriteByte(' ')
		}
		usec := iv.Microseconds
		switch {
		case usec < 0:
			b.WriteByte('-')
		case isBefore:
			b.WriteByte('+')
		}
		// Negated as uint64 so math.MinInt64 does not overflow.
		mag := uint64(usec)
		if usec < 0 {
			mag = -mag
		}
		writeClock(&b, mag)
	}
	return b.String()
}

// writeClock writes usec as HH:MM:SS[.ffffff] with the fraction trimmed.
func writeClock(b *strings.Builder, usec uint64) {
	writeTwoDigits(b, usec/usecPerHour)
	b.WriteByte(':')
	writeTwoDigits(b, usec%usecPerHour/usecPerMinute)
	b.WriteByte(':')
	writeTwoDigits(b, usec%usecPerMinute/usecPerSecond)
	if frac := usec % usecPerSecond; frac != 0 {
		digits := strconv.FormatUint(frac+usecPerSecond, 10)[1:]
		b.WriteByte('.')
		b.WriteString(strings.TrimRight(digits, "0"))
	}
}

func writeTwoDigits(b *strings.Builder, n uint64) {
	if n < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.FormatUint(n, 10))
}
