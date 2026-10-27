package pgserverless

import (
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

// The expected strings are what time_out and interval_out print under the
// default IntervalStyle, including the explicit "+" after a negative field.
func TestClockTextMatchesPostgres(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"time drops a zero fraction", pgtype.Time{Microseconds: 45296000000, Valid: true}, "12:34:56"},
		{"time trims trailing zeros", pgtype.Time{Microseconds: 45296500000, Valid: true}, "12:34:56.5"},
		{"time keeps a single microsecond", pgtype.Time{Microseconds: 1, Valid: true}, "00:00:00.000001"},
		{"time midnight", pgtype.Time{Valid: true}, "00:00:00"},
		{"time end of day", pgtype.Time{Microseconds: 86400000000, Valid: true}, "24:00:00"},

		{"zero interval", pgtype.Interval{Valid: true}, "00:00:00"},
		{"one day has no time part", pgtype.Interval{Days: 1, Valid: true}, "1 day"},
		{"plural day", pgtype.Interval{Days: 2, Valid: true}, "2 days"},
		{"one month", pgtype.Interval{Months: 1, Valid: true}, "1 mon"},
		{"every field", pgtype.Interval{Months: 14, Days: 3, Microseconds: 14706700000, Valid: true}, "1 year 2 mons 3 days 04:05:06.7"},
		{"negative one day is plural", pgtype.Interval{Days: -1, Valid: true}, "-1 days"},
		{"positive time after a negative day", pgtype.Interval{Days: -1, Microseconds: 7200000000, Valid: true}, "-1 days +02:00:00"},
		{"negative time after a positive day", pgtype.Interval{Days: 1, Microseconds: -7200000000, Valid: true}, "1 day -02:00:00"},
		{"positive day after a negative month", pgtype.Interval{Months: -1, Days: 1, Valid: true}, "-1 mons +1 day"},
		{"negative months split into years", pgtype.Interval{Months: -14, Valid: true}, "-1 years -2 mons"},
		{"negative fraction only", pgtype.Interval{Microseconds: -500000, Valid: true}, "-00:00:00.5"},
		{"hours past a day stay hours", pgtype.Interval{Microseconds: 360000000000, Valid: true}, "100:00:00"},
		{"infinity", pgtype.Interval{Months: math.MaxInt32, Days: math.MaxInt32, Microseconds: math.MaxInt64, Valid: true}, "infinity"},
		{"minus infinity", pgtype.Interval{Months: math.MinInt32, Days: math.MinInt32, Microseconds: math.MinInt64, Valid: true}, "-infinity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clockText(tt.v)
			assert.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClockTextLeavesOtherValuesToTheCaller(t *testing.T) {
	for _, v := range []any{"12:34:56", pgtype.Time{}, pgtype.Interval{}, int64(1)} {
		_, ok := clockText(v)
		assert.False(t, ok, "%#v", v)
	}
}

func TestCoerceNeonValue_ClockArraysAreTextElementWise(t *testing.T) {
	m := pgtype.NewMap()
	assert.Equal(t,
		[]any{"1 day", nil},
		coerceNeonValue(m, []any{pgtype.Interval{Days: 1, Valid: true}, nil}, oidIntervalArray))
	assert.Equal(t,
		[]any{"12:34:56"},
		coerceNeonValue(m, []any{pgtype.Time{Microseconds: 45296000000, Valid: true}}, oidTimeArray))
}

// Unlike pgTextValue, the clock types need no type map.
func TestCoerceNeonValue_ClockWithoutATypeMap(t *testing.T) {
	assert.Equal(t, any("1 day"), coerceNeonValue(nil, pgtype.Interval{Days: 1, Valid: true}, oidInterval))
}
