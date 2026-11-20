package util

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk/v3/log"
)

// MomentForms lists the forms ParseMoment accepts, an example of each, for
// the help of a flag and for the error on a value of none of them.
const MomentForms = `  1731592956.818                 Unix seconds, integer or fractional
  2024-11-14T14:02:36.818Z       RFC 3339, with a zone: Z, +hh:mm or -hh:mm
  2024-11-14 17:02:36.818+03:00  the same with a space for the T
  2024-11-14 17:02:36.818        no zone: local time; T for the space too
  2024-11-14 17:02               to the minute, local time
  2024-11-14                     midnight, local time`

var (
	errNotAMoment = errors.New("is not a moment in time")
	errMomentFar  = errors.New("is out of the years 0000-9999")
)

// momentLayouts are the layouts of a date and time ParseMoment tries, the
// ones with a zone first. A fraction of a second is not in them: time.Parse
// takes one of any length after the seconds by itself.
var momentLayouts = []struct {
	layout string
	local  bool
}{
	{"2006-01-02T15:04:05Z07:00", false},
	{"2006-01-02 15:04:05Z07:00", false},
	{"2006-01-02T15:04:05", true},
	{"2006-01-02 15:04:05", true},
	{"2006-01-02T15:04", true},
	{"2006-01-02 15:04", true},
	{"2006-01-02", true},
}

// The Unix seconds of the first and the last moments time.Parse reads a date
// of: years beyond 0000-9999 take more than four digits.
var (
	firstMomentUnix = time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
	lastMomentUnix  = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
)

// plainDecimal is a number big.Rat reads exactly and fast: a sign, digits and
// a point. The other numbers of strconv.ParseFloat, with an exponent that may
// take big.Rat long or in hexadecimal, are taken as the float they are.
var plainDecimal = regexp.MustCompile(`^[+-]?[0-9]*\.?[0-9]*$`)

// Moment is a moment in time given on the command line.
type Moment struct {
	// Time is the moment, in the zone it was given in: UTC for Unix seconds,
	// the local one for a time without a zone.
	Time time.Time
	// Unix is the moment in seconds since the Unix epoch. For Unix seconds
	// it is the number as strconv.ParseFloat reads it. For a date and time it
	// is the seconds with a nine-digit fraction read the same way, so that
	// 36.0270 seconds past a minute is .027, not .27.
	Unix float64
	// Local reports that the value named no zone and was read in local time.
	Local bool
}

// ParseMoment reads a moment in time given on the command line, in one of
// MomentForms. A date and time without a zone is local time: that of
// time.Local, which honours the TZ environment variable, as date and
// journalctl --since do.
func ParseMoment(input string) (Moment, error) {
	return ParseMomentIn(input, time.Local) //nolint:gosmopolitan // Local time is the point.
}

// ParseMomentIn is ParseMoment with loc for the local time.
//
// A local time the clocks skip or show twice at a change of daylight saving
// time is read the way time.Date reads it: with the offset of one side of
// the change, which one Go does not promise. Go 1.27 reads 02:30 of the
// America/New_York spring gap with the summer offset, as 01:30 EST, and
// 01:30 of its autumn overlap as the first one, EDT; in a zone east of
// Greenwich it is the other way round. A value with a zone is exact.
func ParseMomentIn(input string, loc *time.Location) (Moment, error) {
	// Infinity and NaN are numbers to strconv.ParseFloat, not moments.
	seconds, err := strconv.ParseFloat(input, 64)
	if err == nil && !math.IsInf(seconds, 0) && !math.IsNaN(seconds) {
		return unixMoment(input, seconds)
	}

	var rangeErr error

	for _, form := range momentLayouts {
		moment, err := time.ParseInLocation(form.layout, input, loc)
		if err == nil {
			return Moment{Time: moment, Unix: unixSeconds(moment), Local: form.local}, nil
		}

		// A value of the right form with a field out of range, the 30th of
		// February, is better told so than given the list of forms: the
		// error is `parsing time "2024-02-30": day out of range`.
		var parseErr *time.ParseError

		if rangeErr == nil && errors.As(err, &parseErr) &&
			strings.HasSuffix(parseErr.Message, " out of range") {
			rangeErr = parseErr
		}
	}

	if rangeErr != nil {
		return Moment{}, rangeErr
	}

	return Moment{}, fmt.Errorf("%q %w; give one of:\n%s", input, errNotAMoment, MomentForms)
}

// LogLocalMoment logs at debug level the moment in UTC a value of flag was
// read as, when the value named no zone: a mistaken TZ shows there.
func LogLocalMoment(flag, input string, moment Moment) {
	if !moment.Local {
		return
	}

	log.Debugf("%s %q names no zone, read as local time %s, that is %s",
		flag, input, moment.Time.Format("2006-01-02 15:04:05.999999999 -07:00 MST"),
		moment.Time.UTC().Format(time.RFC3339Nano))
}

// unixMoment is the moment of Unix seconds the input gives and
// strconv.ParseFloat has read as seconds.
func unixMoment(input string, seconds float64) (Moment, error) {
	if seconds < float64(firstMomentUnix) || seconds >= float64(lastMomentUnix) {
		return Moment{}, fmt.Errorf("%q %w", input, errMomentFar)
	}

	// The float is not what Time is made of: 1731592956.818 is 78 ns
	// past .818 as a float64, and a decimal is read exactly.
	nanos, ok := new(big.Rat), false
	if plainDecimal.MatchString(input) {
		_, ok = nanos.SetString(input)
	}

	if !ok {
		nanos.SetFloat64(seconds)
	}

	nanos.Mul(nanos, big.NewRat(int64(time.Second), 1))

	// A fraction finer than a nanosecond is dropped towards the past: Div of
	// a positive divisor rounds down, unlike Quo.
	whole := new(big.Int).Div(nanos.Num(), nanos.Denom())
	sec, nsec := new(big.Int).DivMod(whole, big.NewInt(int64(time.Second)), new(big.Int))

	return Moment{
		Time:  time.Unix(sec.Int64(), nsec.Int64()).UTC(),
		Unix:  seconds,
		Local: false,
	}, nil
}

// unixSeconds is moment in seconds since the Unix epoch: the seconds with a
// nine-digit fraction, read by strconv.ParseFloat. A moment before the epoch
// is negative as a whole: Unix() and Nanosecond() of half a second before it
// are -1 and 500000000, and the seconds are -0.500000000, not -1.500000000.
func unixSeconds(moment time.Time) float64 {
	sec, nsec := moment.Unix(), int64(moment.Nanosecond())

	sign := ""
	if sec < 0 {
		sign = "-"

		if nsec > 0 {
			sec++

			nsec = int64(time.Second) - nsec
		}

		sec = -sec
	}

	// A decimal of digits always parses.
	seconds, _ := strconv.ParseFloat(fmt.Sprintf("%s%d.%09d", sign, sec, nsec), 64)

	return seconds
}
