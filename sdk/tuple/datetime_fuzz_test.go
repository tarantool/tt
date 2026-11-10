package tuple_test

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"

	"github.com/tarantool/tt/sdk/tuple"
)

// The range of the seconds of a datetime go-tarantool takes.
const (
	minDatetimeSeconds = -185604722870400
	maxDatetimeSeconds = 185480451417600
)

// datetimeTexts are texts of datetimes for the seed corpora, as Tarantool
// prints them, as RFC 3339 writes them, and as AppendTaggedJSON writes those
// their text does not tell.
var datetimeTexts = []string{
	"2024-11-14", "2024-11-14T14:02", "2024-11-14 14:02:36", "2024-11-14t14:02:36.5z",
	"2024-11-14T14:02:36,123456+03:00", "2024-11-14T14:02:36.000000005-0130",
	"2024-11-14T14:02:36+03", "2024-11-14T14:02:36 Europe/Moscow", "2024-11-14T14:02:36 MSK",
	"2024-11-14T14:02:36 UTC", "2024-11-14T14:02:36A", "2024-11-14 Europe/Moscow",
	"-001-01-01T00:00:00Z", "+12345-06-07T08:09:10+1400", "2024-10-27T02:30:00+0100 Europe/Berlin",
	"2024-10-27T02:30:00+0200 Europe/Berlin", "2024-03-31T02:30:00 Europe/Berlin",
	"2024-11-03T01:30:00 America/New_York", "2024-11-14T00:00:00+0000 Z",
	"2011-12-30T12:00:00 Pacific/Apia", "2024-02-29", "2023-02-29", "0000-01-01T00:00:00Z",
	"9999-12-31T23:59:59.999999999+14:00", "2024-11-14T14:02:60Z", "2024-11-14T23:59:59-12:00",
	"-5879610-06-22T00:00:00Z", "5879611-07-11T00:00:00Z", "2024-11-14T14:02:36 Mars/Olympus",
	"2024-11-14T14:02:36.1234567890Z", "1970-01-01T00:00:00 Etc/GMT+12",
}

// rfc3339 is the grammar of a date-time of RFC 3339.
var rfc3339 = regexp.MustCompile(
	`^\d{4}-\d\d-\d\d[Tt]\d\d:\d\d:\d\d(\.\d+)?([Zz]|[+-]\d\d:\d\d)$`)

// rfc3339Fraction finds the fraction of a second of an RFC 3339 text.
var rfc3339Fraction = regexp.MustCompile(`:\d\d[.,](\d*)`)

// datetimeHead reads the date and the time at the start of the text of a
// datetime.
var datetimeHead = regexp.MustCompile(
	`^([+-]?\d{1,7})-(\d\d)-(\d\d)(?:[Tt ](\d\d):(\d\d)(?::(\d\d)(?:[.,](\d{1,9}))?)?)?`)

// FuzzParseDatetime checks that ParseDatetime reports only its error, that
// the texts of what it reads are fixed points, that it reads the date it is
// given and RFC 3339 as time.Parse does.
func FuzzParseDatetime(f *testing.F) {
	for _, seed := range datetimeTexts {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		value, err := tuple.ParseDatetime(text)

		checkRFC3339(t, text, value, err)

		if err != nil {
			require.ErrorIs(t, err, tuple.ErrDatetime)

			return
		}

		checkDateOracle(t, text, value)
		checkDatetimeTexts(t, value)
	})
}

// checkRFC3339 checks that a text time.Parse reads as RFC 3339, of a year
// and an offset a datetime has, is the same instant and offset to
// ParseDatetime.
func checkRFC3339(t *testing.T, text string, value datetime.Datetime, err error) {
	t.Helper()

	// time.Parse takes more than RFC 3339, such as an hour of one digit.
	if !rfc3339.MatchString(text) {
		return
	}

	moment, parseErr := time.Parse(time.RFC3339Nano, text)
	if parseErr != nil || moment.Year() < 0 || moment.Year() > 9999 {
		return
	}

	_, offset := moment.Zone()
	if offset < -12*3600 || offset > 14*3600 {
		return
	}

	// Go reads a fraction of any length, ParseDatetime of nine digits.
	if match := rfc3339Fraction.FindStringSubmatch(text); match != nil && len(match[1]) > 9 {
		return
	}

	require.NoError(t, err, "time.Parse reads %q as %s", text, moment)

	got := value.ToTime()
	_, gotOffset := got.Zone()

	require.True(t, got.Equal(moment), "%q: %s, time.Parse %s", text, got, moment)
	require.Equal(t, offset, gotOffset, text)
	require.Equal(t, datetime.NoTimezone, got.Location().String(), text)
}

// checkDateOracle checks that ParseDatetime reads the date and the time the
// text starts with: exactly in a zone of a fixed offset, and in a zone of the
// database moved by no more than a gap the zone has - the hour of summer
// time, or the day a zone skipped, as Samoa skipped 2011-12-30.
func checkDateOracle(t *testing.T, text string, value datetime.Datetime) {
	t.Helper()

	want, day, ok := textWall(text)
	require.True(t, ok, "%q is read with no date", text)
	require.Equal(t, day, want.Day(), "%q is read as a day the month has not", text)

	moment := value.ToTime()
	got := wallClock(moment)

	abbrev, _ := moment.Zone()
	if abbrev == moment.Location().String() {
		require.True(t, got.Equal(want), "%q is read as %s", text, got)

		return
	}

	const gap = 27 * time.Hour

	shift := got.Sub(want)
	require.True(t, shift >= -gap && shift <= gap, "%q is read as %s", text, got)
}

// textWall is the date and the time the text of a datetime starts with, as
// a time in UTC, and the day of the text.
func textWall(text string) (time.Time, int, bool) {
	match := datetimeHead.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}, 0, false
	}

	parts := make([]int, len(match)-1)

	for index, part := range match[1:] {
		if part == "" {
			continue
		}

		number, err := strconv.Atoi(part)
		if err != nil {
			return time.Time{}, 0, false
		}

		if index == len(parts)-1 {
			number *= int(math.Pow10(9 - len(part)))
		}

		parts[index] = number
	}

	return time.Date(parts[0], time.Month(parts[1]), parts[2], parts[3], parts[4], parts[5],
		parts[6], time.UTC), parts[2], true
}

// wallClock is the date and the time of moment as a time in UTC.
func wallClock(moment time.Time) time.Time {
	return time.Date(moment.Year(), moment.Month(), moment.Day(), moment.Hour(),
		moment.Minute(), moment.Second(), moment.Nanosecond(), time.UTC)
}

// taggedDatetime is the text AppendTaggedJSON writes a datetime as.
func taggedDatetime(t *testing.T, value datetime.Datetime) string {
	t.Helper()

	out, err := tuple.AppendTaggedJSON(nil, value)
	require.NoError(t, err)

	var tagged map[string]string

	require.NoError(t, json.Unmarshal(out, &tagged), "%q", out)
	require.Len(t, tagged, 1, "%q", out)

	text, ok := tagged[tuple.TagDatetime]
	require.True(t, ok, "%q", out)

	return text
}

// checkDatetimeTexts checks the texts of a datetime: its Text reads back,
// and is a fixed point once read back; its tagged JSON is its Text when
// that reads back as it, and reads back as it in any case; its Lua reads
// back as its Text does, with the zone of one letter it has.
func checkDatetimeTexts(t *testing.T, value datetime.Datetime) {
	t.Helper()

	text, ok := tuple.Text(value)
	require.True(t, ok)

	back, err := tuple.ParseDatetime(text)
	if err != nil && slices.Contains(ambiguousZones, value.ToTime().Location().String()) {
		// Like Tarantool, ParseDatetime takes no ambiguous abbreviation
		// without an offset; the tagged form has one.
		require.NotEqual(t, text, taggedDatetime(t, value))
		checkTaggedJSON(t, value)

		return
	}

	if err != nil {
		// At the edges of the range of a datetime, its wall clock at the
		// offset its zone has may be out of the range.
		const edge = 2 * 24 * 3600

		seconds := value.ToTime().Unix()
		require.True(t, seconds < minDatetimeSeconds+edge || seconds > maxDatetimeSeconds-edge,
			"the Text %q does not read back: %v", text, err)
		require.NotEqual(t, text, taggedDatetime(t, value))
		checkTaggedJSON(t, value)

		return
	}

	lossless := sameDatetime(back, value)
	backText, _ := tuple.Text(back)

	if lossless {
		require.Equal(t, text, backText)
	} else {
		again, err := tuple.ParseDatetime(backText)
		require.NoError(t, err, backText)

		againText, _ := tuple.Text(again)
		require.Equal(t, backText, againText, "the Text of %q is not a fixed point", text)
	}

	tagged := taggedDatetime(t, value)
	if lossless {
		require.Equal(t, text, tagged, "the Text reads back, but the tagged JSON is not it")
	} else {
		require.NotEqual(t, text, tagged, "the Text does not read back, yet is the tagged JSON")
	}

	checkTaggedJSON(t, value)

	literal := string(tuple.AppendLua(nil, value))
	inner, ok := strings.CutPrefix(literal, "(require('datetime').parse(")
	require.True(t, ok, literal)

	luaText, err := unquoteLua(strings.TrimSuffix(inner, "))"))
	require.NoError(t, err, literal)

	luaBack, err := tuple.ParseDatetime(luaText)
	require.NoError(t, err, literal)

	luaBackText, _ := tuple.Text(luaBack)
	require.Equal(t, backText, luaBackText, literal)

	if zone := value.ToTime().Location().String(); len(zone) == 1 {
		require.Equal(t, zone, luaBack.ToTime().Location().String(), literal)
	}
}

// datetimeFieldOptions are the options a datetime is written with by
// FuzzDatetimeValue, null among them its Text.
func datetimeFieldOptions(text string) []tuple.FieldOptions {
	return []tuple.FieldOptions{
		{},
		{Null: text},
		{ThousandSeparators: " `", DecimalSeparators: ".,"},
		{Unsupported: []int8{tuple.ExtDatetime}},
		{Null: strconv.Quote(text)},
	}
}

// checkDatetimeFields checks that a datetime reads back from every field
// that holds it.
func checkDatetimeFields(t *testing.T, value any, text string) {
	t.Helper()

	for _, opts := range datetimeFieldOptions(text) {
		for _, typ := range []tuple.FieldType{
			tuple.FieldDatetime, tuple.FieldAny, tuple.FieldScalar,
		} {
			for _, nullable := range []bool{false, true} {
				field := tuple.Field{Name: "f", Type: typ, IsNullable: nullable}
				checkFieldRoundTrip(t, value, field, opts)
			}
		}
	}
}

// datetimeZoneCount is the number of zones FuzzDatetimeValue picks from:
// none, those of the database and the abbreviations.
var datetimeZoneCount = 1 + len(datetimeZones) + len(zoneAbbreviations)

// fuzzLocation is the location FuzzDatetimeValue picks: a zone at its own
// offset or, when fixed, at offset minutes.
func fuzzLocation(t *testing.T, choice int, offset int, fixed bool) *time.Location {
	t.Helper()

	switch {
	case choice == 0:
		return time.FixedZone(datetime.NoTimezone, offset*60)
	case choice <= len(datetimeZones):
		name := datetimeZones[choice-1]
		if fixed {
			return time.FixedZone(name, offset*60)
		}

		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Skipf("no time zone %s", name)
		}

		return loc
	}

	abbrev := zoneAbbreviations[choice-1-len(datetimeZones)]
	if fixed {
		return time.FixedZone(abbrev.name, offset*60)
	}

	return time.FixedZone(abbrev.name, abbrev.offset*60)
}

// FuzzDatetimeValue checks that a datetime of any instant, zone and offset
// reads back from its tagged JSON and from every field that holds it.
func FuzzDatetimeValue(f *testing.F) {
	for _, seconds := range datetimeSeconds {
		for choice := range datetimeZoneCount {
			f.Add(seconds, int64(0), uint8(choice), int16(60), choice%3 == 0)
		}
	}

	f.Fuzz(func(t *testing.T, seconds, nanos int64, zone uint8, offset int16, fixed bool) {
		if seconds < minDatetimeSeconds || seconds > maxDatetimeSeconds {
			seconds %= maxDatetimeSeconds
		}

		minutes := (int(offset)%(26*60+1)+26*60+1)%(26*60+1) - 12*60
		loc := fuzzLocation(t, int(zone)%datetimeZoneCount, minutes, fixed)

		moment, err := datetime.NewDatetime(time.Unix(seconds, nanos%1_000_000_000).In(loc))
		if err != nil {
			return
		}

		value := decodeModel(t, moment)
		typed, ok := value.(datetime.Datetime)
		require.True(t, ok, describe(value))
		skipKnownBugs(t, typed)

		checkDatetimeTexts(t, typed)

		text, _ := tuple.Text(typed)
		checkDatetimeFields(t, typed, text)
	})
}

// legacyInterval reads the nine integers go-tupleconv writes an interval as.
var legacyInterval = regexp.MustCompile(`^(-?\d+)(?:,(-?\d+)){8}$`)

// FuzzParseInterval checks that ParseInterval reports only its error and
// that what it reads has a Text that reads back as it, and reads back from
// tagged JSON and from every field that holds it.
func FuzzParseInterval(f *testing.F) {
	for _, seed := range []string{
		"+0 seconds", "0 seconds, adjust none", "+1 years, -2 seconds", "-2 seconds, 1 year",
		"+1 weeks, 2 days, 3 hours, 4 minutes, 5 nanoseconds, adjust excess",
		"1,2,3,4,5,6,7,8,1", "1,2,3,4,5,6,7,8,3", "+1 years, adjust 7", "adjust last",
		"-9223372036854775808 years, 9223372036854775807 nanoseconds", "+1 years,-2 seconds",
		"+1 parsecs", "+01 year", "1 minute, 1 minutes", "", " ", ",,,,,,,,",
		"+0 seconds, adjust 0",
		"0,0,0,0,0,0,0,0,+1", "+1 year, -0 seconds",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		value, err := tuple.ParseInterval(text)
		if err != nil {
			require.ErrorIs(t, err, tuple.ErrInterval)

			return
		}

		if legacyInterval.MatchString(text) {
			checkLegacyInterval(t, text, value)
		}

		checkIntervalTexts(t, value)
	})
}

// checkLegacyInterval checks that ParseInterval reads the text go-tupleconv
// writes as its nine integers, if the last is an adjust mode.
func checkLegacyInterval(t *testing.T, text string, value datetime.Interval) {
	t.Helper()

	parts := strings.Split(text, ",")
	numbers := make([]int64, len(parts))

	for index, part := range parts {
		number, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return
		}

		numbers[index] = number
	}

	if numbers[8] < 0 || numbers[8] > 2 {
		return
	}

	require.Equal(t, datetime.Interval{
		Year: numbers[0], Month: numbers[1], Week: numbers[2], Day: numbers[3],
		Hour: numbers[4], Min: numbers[5], Sec: numbers[6], Nsec: numbers[7],
		Adjust: datetime.Adjust(numbers[8]),
	}, value, text)
}

// checkIntervalTexts checks that the Text of an interval reads back as it
// and is a fixed point, and that it reads back from tagged JSON and from
// every field that holds it.
func checkIntervalTexts(t *testing.T, value datetime.Interval) {
	t.Helper()

	text, ok := tuple.Text(value)
	require.True(t, ok)

	back, err := tuple.ParseInterval(text)
	require.NoError(t, err, text)
	require.Equal(t, value, back, text)

	backText, _ := tuple.Text(back)
	require.Equal(t, text, backText)

	checkTaggedJSON(t, value)

	for _, opts := range []tuple.FieldOptions{
		{}, {Null: text}, {ThousandSeparators: " `", DecimalSeparators: ".,"},
		{Unsupported: []int8{tuple.ExtInterval}}, {Null: "0"},
	} {
		for _, typ := range []tuple.FieldType{tuple.FieldInterval, tuple.FieldAny} {
			for _, nullable := range []bool{false, true} {
				field := tuple.Field{Name: "f", Type: typ, IsNullable: nullable}
				checkFieldRoundTrip(t, value, field, opts)
			}
		}
	}
}

// FuzzIntervalValue checks that an interval of any units and adjust mode
// reads back from its Text, its tagged JSON, the text go-tupleconv writes
// and every field that holds it, and, of an adjust mode Tarantool has, from
// its MessagePack.
func FuzzIntervalValue(f *testing.F) {
	f.Add(int64(1), int64(-2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8),
		uint8(1))
	f.Add(int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0),
		uint8(0))
	f.Add(int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(5),
		uint8(2))
	f.Add(int64(math.MinInt64), int64(math.MaxInt64), int64(0), int64(0), int64(0), int64(0),
		int64(math.MinInt64), int64(0), uint8(7))
	f.Add(int64(-1), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0),
		uint8(255))

	f.Fuzz(func(t *testing.T, year, month, week, day, hour, minute, second, nanos int64,
		adjust uint8,
	) {
		value := datetime.Interval{
			Year: year, Month: month, Week: week, Day: day, Hour: hour, Min: minute,
			Sec: second, Nsec: nanos, Adjust: datetime.Adjust(adjust),
		}

		checkIntervalTexts(t, value)

		if adjust > 2 {
			return
		}

		require.Equal(t, value, decodeModel(t, value))

		legacy := fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d", year, month, week, day, hour, minute,
			second, nanos, adjust)

		back, err := tuple.ParseInterval(legacy)
		require.NoError(t, err, legacy)
		require.Equal(t, value, back, legacy)

		guessed, err := tuple.ParseField(legacy, tuple.Field{Name: "f", Type: tuple.FieldAny},
			tuple.FieldOptions{})
		require.NoError(t, err, legacy)
		requireSame(t, value, guessed, legacy)
	})
}
