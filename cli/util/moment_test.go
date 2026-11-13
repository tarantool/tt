package util_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // The zones of the tests, wherever they run.

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/util"
)

// loadZone is a zone of the tests: they do not depend on that of the machine.
func loadZone(t *testing.T, name string) *time.Location {
	t.Helper()

	zone, err := time.LoadLocation(name)
	require.NoError(t, err)

	return zone
}

// unixOf is seconds as tt data reads them, the decimal parsed by
// strconv.ParseFloat.
func unixOf(t *testing.T, seconds string) float64 {
	t.Helper()

	unix, err := strconv.ParseFloat(seconds, 64)
	require.NoError(t, err)

	return unix
}

func TestParseMomentIn(t *testing.T) {
	// Moscow is three hours east of UTC all year since 2014, New York is
	// where a zone in the value has to win over the local one.
	moscow := loadZone(t, "Europe/Moscow")
	newYork := loadZone(t, "America/New_York")

	tests := []struct {
		name  string
		input string
		loc   *time.Location
		// time is the moment in UTC.
		time time.Time
		// unix is the seconds since the epoch, as a decimal.
		unix  string
		local bool
	}{
		{
			name:  "unix_integer",
			input: "123456",
			time:  time.Date(1970, 1, 2, 10, 17, 36, 0, time.UTC),
			unix:  "123456",
		},
		{
			name:  "unix_fraction",
			input: "123456.123",
			time:  time.Date(1970, 1, 2, 10, 17, 36, 123000000, time.UTC),
			unix:  "123456.123",
		},
		{
			// As a float64 it is 78 ns past .818: the time is the decimal.
			name:  "unix_fraction_exact",
			input: "1731592956.818",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 818000000, time.UTC),
			unix:  "1731592956.818",
		},
		{
			name:  "unix_before_epoch",
			input: "-0.5",
			time:  time.Date(1969, 12, 31, 23, 59, 59, 500000000, time.UTC),
			unix:  "-0.5",
		},
		{
			name:  "unix_exponent",
			input: "1.7e9",
			time:  time.Unix(1700000000, 0).UTC(),
			unix:  "1700000000",
		},
		{
			name:  "rfc3339_offset_zero",
			input: "2022-04-28T07:22:12+00:00",
			time:  time.Date(2022, 4, 28, 7, 22, 12, 0, time.UTC),
			unix:  "1651130532",
		},
		{
			name:  "rfc3339_z",
			input: "2022-04-28T07:22:12.1534Z",
			time:  time.Date(2022, 4, 28, 7, 22, 12, 153400000, time.UTC),
			unix:  "1651130532.1534",
		},
		{
			// The leading zero of the fraction is kept: .0270 is 27
			// milliseconds, not 270.
			name:  "rfc3339_leading_zero",
			input: "2024-11-14T14:02:36.0270+00:00",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 27000000, time.UTC),
			unix:  "1731592956.027",
		},
		{
			name:  "rfc3339_nanosecond",
			input: "2024-11-14T14:02:36.000000001Z",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 1, time.UTC),
			unix:  "1731592956.000000001",
		},
		{
			name:  "rfc3339_offset",
			input: "2024-11-14T17:02:36.5+03:00",
			loc:   newYork,
			time:  time.Date(2024, 11, 14, 14, 2, 36, 500000000, time.UTC),
			unix:  "1731592956.5",
		},
		{
			// Half a second before the epoch is -0.5, whereas its Unix()
			// is -1 and its Nanosecond() 500000000.
			name:  "rfc3339_before_epoch",
			input: "1969-12-31T23:59:59.5Z",
			time:  time.Date(1969, 12, 31, 23, 59, 59, 500000000, time.UTC),
			unix:  "-0.5",
		},
		{
			name:  "space_zone",
			input: "2024-11-14 17:02:36.8181+03:00",
			loc:   newYork,
			time:  time.Date(2024, 11, 14, 14, 2, 36, 818100000, time.UTC),
			unix:  "1731592956.8181",
		},
		{
			name:  "space_z",
			input: "2024-11-14 14:02:36.8181Z",
			loc:   moscow,
			time:  time.Date(2024, 11, 14, 14, 2, 36, 818100000, time.UTC),
			unix:  "1731592956.8181",
		},
		{
			name:  "local_space_fraction",
			input: "2024-11-14 17:02:36.8181",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 818100000, time.UTC),
			unix:  "1731592956.8181",
			local: true,
		},
		{
			name:  "local_t_fraction",
			input: "2024-11-14T17:02:36.8181",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 818100000, time.UTC),
			unix:  "1731592956.8181",
			local: true,
		},
		{
			name:  "local_one_digit",
			input: "2024-11-14 17:02:36.8",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 800000000, time.UTC),
			unix:  "1731592956.8",
			local: true,
		},
		{
			name:  "local_nine_digits",
			input: "2024-11-14 17:02:36.123456789",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 123456789, time.UTC),
			unix:  "1731592956.123456789",
			local: true,
		},
		{
			name:  "local_seconds",
			input: "2024-11-14 17:02:36",
			time:  time.Date(2024, 11, 14, 14, 2, 36, 0, time.UTC),
			unix:  "1731592956",
			local: true,
		},
		{
			name:  "local_minutes",
			input: "2024-11-14 17:02",
			time:  time.Date(2024, 11, 14, 14, 2, 0, 0, time.UTC),
			unix:  "1731592920",
			local: true,
		},
		{
			name:  "local_t_minutes",
			input: "2024-11-14T17:02",
			time:  time.Date(2024, 11, 14, 14, 2, 0, 0, time.UTC),
			unix:  "1731592920",
			local: true,
		},
		{
			name:  "local_date",
			input: "2024-11-14",
			time:  time.Date(2024, 11, 13, 21, 0, 0, 0, time.UTC),
			unix:  "1731531600",
			local: true,
		},
		{
			name:  "local_new_york",
			input: "2024-11-14 09:02:36.8181",
			loc:   newYork,
			time:  time.Date(2024, 11, 14, 14, 2, 36, 818100000, time.UTC),
			unix:  "1731592956.8181",
			local: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := tt.loc
			if loc == nil {
				loc = moscow
			}

			moment, err := util.ParseMomentIn(tt.input, loc)
			require.NoError(t, err)
			assert.Equal(t, tt.time, moment.Time.UTC())
			assert.InDelta(t, unixOf(t, tt.unix), moment.Unix, 0)
			assert.Equal(t, tt.local, moment.Local)

			if tt.local {
				assert.Equal(t, loc, moment.Time.Location())
			}
		})
	}
}

// TestParseMomentInDST pins what time.Date makes of the local times a change of
// daylight saving time skips or repeats. Go does not promise it: the test is
// here to tell when it changes, not to promise it either.
func TestParseMomentInDST(t *testing.T) {
	newYork := loadZone(t, "America/New_York")

	// The clocks went from 02:00 EST to 03:00 EDT: 02:30 never happened. It
	// is read with the summer offset, as 01:30 EST.
	moment, err := util.ParseMomentIn("2024-03-10 02:30", newYork)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2024, 3, 10, 6, 30, 0, 0, time.UTC), moment.Time.UTC())

	// The clocks went from 02:00 EDT back to 01:00 EST: 01:30 happened twice.
	// It is read as the first, EDT.
	moment, err = util.ParseMomentIn("2024-11-03 01:30", newYork)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2024, 11, 3, 5, 30, 0, 0, time.UTC), moment.Time.UTC())

	// A zone in the value makes it exact: the second 01:30.
	moment, err = util.ParseMomentIn("2024-11-03 01:30:00-05:00", newYork)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2024, 11, 3, 6, 30, 0, 0, time.UTC), moment.Time.UTC())
}

// TestParseMomentLocal checks ParseMoment takes time.Local for the local time,
// which is what TZ sets.
//
//nolint:gosmopolitan // Setting time.Local is what the test is for.
func TestParseMomentLocal(t *testing.T) {
	saved := time.Local

	t.Cleanup(func() { time.Local = saved })

	time.Local = loadZone(t, "Europe/Moscow")

	moment, err := util.ParseMoment("2024-11-14 17:02:36.8181")
	require.NoError(t, err)
	assert.True(t, moment.Local)
	assert.Equal(t, time.Date(2024, 11, 14, 14, 2, 36, 818100000, time.UTC), moment.Time.UTC())

	time.Local = time.UTC

	moment, err = util.ParseMoment("2024-11-14 17:02:36.8181")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2024, 11, 14, 17, 2, 36, 818100000, time.UTC), moment.Time.UTC())
}

func TestParseMomentInErrors(t *testing.T) {
	forms := []string{
		"",
		"abcdef",
		"yesterday",
		"2024-11-14T14:02:36.abc",
		"2022-0_-28A07:22:12.1534abc", // spell-checker:disable-line
		"2022-04-28T07:22:12.1534abc",
		"2022-04-28T07:22abc",
		"2024-11-14 17",
		"2024-11-14 17:02:36.",
		"2024-11-14 17:02:36 +03:00",
		"2024-11-14 17:02+03:00",
		"2024-11-14T17:02:36+0300",
		"14.11.2024",
		"2024-11",
		"inf",
		"NaN",
		"1e400",
	}

	for _, input := range forms {
		t.Run(input, func(t *testing.T) {
			_, err := util.ParseMomentIn(input, time.UTC)
			require.Error(t, err)
			assert.Equal(t,
				strconv.Quote(input)+" is not a moment in time; give one of:\n"+util.MomentForms,
				err.Error())
		})
	}

	ranges := map[string]string{
		"2024-02-30":                "day out of range",
		"2024-13-01 10:00":          "month out of range",
		"2024-11-14 25:00":          "hour out of range",
		"2024-11-14 17:61:00":       "minute out of range",
		"2024-11-14T17:02:36+25:00": "time zone offset hour out of range",
	}

	for input, reason := range ranges {
		t.Run(input, func(t *testing.T) {
			_, err := util.ParseMomentIn(input, time.UTC)
			require.Error(t, err)
			assert.Equal(t, "parsing time "+strconv.Quote(input)+": "+reason, err.Error())
		})
	}

	for _, input := range []string{"1e12", "-1e12", "1e300"} {
		t.Run(input, func(t *testing.T) {
			_, err := util.ParseMomentIn(input, time.UTC)
			require.Error(t, err)
			assert.Equal(t, strconv.Quote(input)+" is out of the years 0000-9999", err.Error())
		})
	}
}

// TestMomentFormsParse checks every example of the list of forms is read, with
// a zone or without one as it says.
func TestMomentFormsParse(t *testing.T) {
	example := regexp.MustCompile(`^  (\S+(?: \S+)?)  +(.*)$`)

	lines := strings.Split(util.MomentForms, "\n")
	require.Len(t, lines, 6)

	for _, line := range lines {
		match := example.FindStringSubmatch(line)
		require.NotNil(t, match, line)

		moment, err := util.ParseMomentIn(match[1], time.UTC)
		require.NoError(t, err, line)
		assert.Equal(t, strings.Contains(match[2], "local time"), moment.Local, line)
	}
}
