package tuple

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tarantool/go-tarantool/v3/datetime"
)

// The units of the text of an interval, in the order Tarantool prints them,
// and the word that names its adjust mode.
var intervalUnits = [...]string{
	"years", "months", "weeks", "days", "hours", "minutes", "seconds", "nanoseconds",
}

const intervalAdjust = "adjust"

// The names of the adjust modes of an interval, as Tarantool names them.
var adjustNames = map[datetime.Adjust]string{
	datetime.NoneAdjust:   "none",
	datetime.ExcessAdjust: "excess",
	datetime.LastAdjust:   "last",
}

// legacyIntervalFields is the number of fields of the text of an interval
// github.com/tarantool/go-tupleconv writes: its eight units and its adjust
// mode, separated by commas.
const legacyIntervalFields = 9

// Limits of the parts of a datetime.
const (
	maxMonth       = 12
	maxDay         = 31
	maxHour        = 23
	maxMinute      = 59
	maxSecond      = 59
	minutesPerHour = 60
	secondsPerMin  = 60
	nsecDigits     = 9
	nanosPerMilli  = 1000_000
	nanosPerMicro  = 1000
)

// The digits of the year Tarantool writes, the sign of a negative one
// included, the most a year of a datetime has (Tarantool takes years from
// -5879610 to 5879611), and the digits of every other part of a datetime.
const (
	yearDigits         = 4
	negativeYearDigits = 3
	maxYearDigits      = 7
	partDigits         = 2
)

var (
	// ErrDatetime reports a text that is not a datetime.
	ErrDatetime = errors.New("not a datetime")
	// ErrInterval reports a text that is not an interval.
	ErrInterval = errors.New("not an interval")
)

// datetimeText is d as Tarantool prints it: the date and the time, the
// fraction of a second in milliseconds, microseconds or nanoseconds, whichever
// is the shortest exact one, and the time zone - its name after a space, or a
// military one-letter name right after the time - or else its offset, Z for
// none.
func datetimeText(d datetime.Datetime) string {
	moment := d.ToTime()
	dst := appendClock(make([]byte, 0, len("2006-01-02T15:04:05.999999999 Europe/Moscow")), moment)

	_, offset := moment.Zone()

	switch zone := moment.Location().String(); {
	case len(zone) == 1:
		dst = append(dst, zone...)
	case zone != datetime.NoTimezone:
		dst = append(dst, ' ')
		dst = append(dst, zone...)
	case offset == 0:
		dst = append(dst, 'Z')
	default:
		dst = appendOffset(dst, offset)
	}

	return string(dst)
}

// appendClock appends the date and the time of moment, with the fraction of
// a second in milliseconds, microseconds or nanoseconds, whichever is the
// shortest exact one.
func appendClock(dst []byte, moment time.Time) []byte {
	dst = fmt.Appendf(dst, "%04d-%02d-%02dT%02d:%02d:%02d", moment.Year(), int(moment.Month()),
		moment.Day(), moment.Hour(), moment.Minute(), moment.Second())

	switch nsec := moment.Nanosecond(); {
	case nsec == 0:
	case nsec%nanosPerMilli == 0:
		dst = fmt.Appendf(dst, ".%03d", nsec/nanosPerMilli)
	case nsec%nanosPerMicro == 0:
		dst = fmt.Appendf(dst, ".%06d", nsec/nanosPerMicro)
	default:
		dst = fmt.Appendf(dst, ".%09d", nsec)
	}

	return dst
}

// appendOffset appends an offset from UTC in seconds as ±HHMM.
func appendOffset(dst []byte, offset int) []byte {
	sign := byte('+')
	if offset < 0 {
		sign, offset = '-', -offset
	}

	minutes := offset / secondsPerMin

	return fmt.Appendf(dst, "%c%02d%02d", sign, minutes/minutesPerHour, minutes%minutesPerHour)
}

// datetimeTaggedText is the text of d that reads back as d: its Text, or,
// where that reads back as another datetime - a time zone named Z, which
// Tarantool prints as no zone, an hour a named zone repeats, an offset the
// zone does not have at the time - the date and the time, the offset and the
// name of the zone after a space: 2024-10-27T02:30:00+0100 Europe/Berlin.
func datetimeTaggedText(value datetime.Datetime) string {
	text := datetimeText(value)

	back, err := ParseDatetime(text)
	if err == nil && sameDatetime(back, value) {
		return text
	}

	moment := value.ToTime()
	_, offset := moment.Zone()

	dst := appendOffset(appendClock(nil, moment), offset)

	if zone := moment.Location().String(); zone != datetime.NoTimezone {
		dst = append(append(dst, ' '), zone...)
	}

	return string(dst)
}

// sameDatetime reports whether two datetimes are the same MessagePack value:
// the same instant, offset and time zone.
func sameDatetime(left, right datetime.Datetime) bool {
	leftTime, rightTime := left.ToTime(), right.ToTime()
	_, leftOffset := leftTime.Zone()
	_, rightOffset := rightTime.Zone()

	return leftTime.Equal(rightTime) && leftOffset == rightOffset &&
		leftTime.Location().String() == rightTime.Location().String()
}

// intervalText is ival as Tarantool prints it - each unit that is not zero,
// the first with its sign, and the seconds when nothing comes before them -
// with its adjust mode after them when it is not none, which Tarantool does
// not print: +1 years, -2 seconds, adjust last.
func intervalText(ival datetime.Interval) string {
	values := [len(intervalUnits)]int64{
		ival.Year, ival.Month, ival.Week, ival.Day, ival.Hour, ival.Min, ival.Sec, ival.Nsec,
	}

	const secondsIndex = 6

	var dst []byte

	for index, value := range values {
		if value == 0 && (index != secondsIndex || len(dst) > 0) {
			continue
		}

		if len(dst) > 0 {
			dst = append(dst, ", "...)
		} else if value >= 0 {
			dst = append(dst, '+')
		}

		dst = strconv.AppendInt(dst, value, decimalBase)
		dst = append(dst, ' ')
		dst = append(dst, intervalUnits[index]...)
	}

	if ival.Adjust != datetime.NoneAdjust {
		name, ok := adjustNames[ival.Adjust]
		if !ok {
			name = strconv.Itoa(int(ival.Adjust))
		}

		dst = append(dst, ", "+intervalAdjust+" "...)
		dst = append(dst, name...)
	}

	return string(dst)
}

// ParseDatetime parses the text of a datetime: a date, YYYY-MM-DD, with a
// year of four to seven digits and a sign when it is negative, then T, t or
// a space and a time, HH:MM, HH:MM:SS or HH:MM:SS with a fraction of up to
// nine digits, and a time zone: Z, an offset in ±HH, ±HHMM or ±HH:MM, a name
// after a space, such as Europe/Moscow or MSK, a military one-letter name, or
// an offset and a name after a space, as [AppendTaggedJSON] writes a
// datetime its Text does not tell. The time and the time zone may be left
// out: the time is midnight then, and the datetime is in UTC with no time
// zone, as in Tarantool. An abbreviation Tarantool knows, such as MSK or
// CET, is its fixed offset, as in Tarantool, and any other name a zone of
// the time zone database; an abbreviation that names more than one zone,
// such as PST or EST, Tarantool does not take, and it is read only with an
// offset.
//
// It reads back the text [Text] writes, which is the one Tarantool prints, and
// RFC 3339.
func ParseDatetime(text string) (datetime.Datetime, error) {
	return parseDatetime(text, false)
}

// datetimeParts are the parts of the text of a datetime.
type datetimeParts struct {
	year, month, day     int
	hour, minute, second int
	nsec                 int
	hasTime, hasZone     bool
	hasOffset            bool
	zone                 string
	offset               int
}

// parseDatetime parses text; strict asks for a time and a time zone, so that
// a date alone or a time with no zone is not taken for a datetime when the
// type of a value is guessed.
func parseDatetime(text string, strict bool) (datetime.Datetime, error) {
	parts, ok := scanDatetime(text)
	if !ok || (strict && (!parts.hasTime || !parts.hasZone)) {
		return datetime.Datetime{}, fmt.Errorf("%q: %w", text, ErrDatetime)
	}

	loc, err := datetimeLocation(parts)
	if err != nil {
		return datetime.Datetime{}, fmt.Errorf("%q: %w", text, err)
	}

	// The day must be one of the month; a day a zone skips, as Samoa skipped
	// 2011-12-30, is a time of the next one, as a time in a gap is.
	lastDay := time.Date(parts.year, time.Month(parts.month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if parts.day > lastDay {
		return datetime.Datetime{}, fmt.Errorf("%q: %w: no such day", text, ErrDatetime)
	}

	moment := time.Date(parts.year, time.Month(parts.month), parts.day,
		parts.hour, parts.minute, parts.second, parts.nsec, loc)

	// Tarantool keeps an offset in whole minutes, the seconds of one the time
	// zone database gives - a local mean time has them - dropped: 1900-01-01
	// in Europe/Moscow is at +0230, not +02:30:17, and -0:25:21 is -0025. The
	// zone is then a fixed one of its name, as Decode gives it.
	if _, offset := moment.Zone(); offset%secondsPerMin != 0 {
		whole := time.FixedZone(loc.String(), offset/secondsPerMin*secondsPerMin)

		moment = time.Date(parts.year, time.Month(parts.month), parts.day,
			parts.hour, parts.minute, parts.second, parts.nsec, whole)
	}

	value, err := datetime.NewDatetime(moment)
	if err != nil {
		return datetime.Datetime{}, fmt.Errorf("%q: %w: %w", text, ErrDatetime, err)
	}

	return value, nil
}

// datetimeLocation is the location of a datetime: its named time zone, as
// the abbreviations of Tarantool or the time zone database give it, at the
// offset the text gives if it gives one, or its offset with no name.
func datetimeLocation(parts datetimeParts) (*time.Location, error) {
	if parts.zone == "" {
		return time.FixedZone(datetime.NoTimezone, parts.offset), nil
	}

	// An ambiguous abbreviation, such as PST, Tarantool does not take in a
	// text, but a datetime of one has its offset, which tells the zone.
	if parts.hasOffset && ambiguousZoneAbbrevs[parts.zone] {
		return time.FixedZone(parts.zone, parts.offset), nil
	}

	loc, err := zoneLocation(parts.zone)
	if err != nil {
		return nil, err
	}

	if parts.hasOffset {
		return time.FixedZone(parts.zone, parts.offset), nil
	}

	return loc, nil
}

// zoneLocations caches the zones of the time zone database zoneLocation
// loads, each a file it would read every time otherwise.
var zoneLocations sync.Map //nolint:gochecknoglobals // A cache.

// zoneLocation is the location of a named time zone: an abbreviation of
// Tarantool, such as MSK, CET or a military one-letter zone, is its fixed
// offset, as Tarantool takes it even where the time zone database knows the
// name with summer time; any other name is a zone of the database.
func zoneLocation(zone string) (*time.Location, error) {
	if offset, ok := zoneAbbrevOffsets[zone]; ok {
		return time.FixedZone(zone, offset*secondsPerMin), nil
	}

	if cached, ok := zoneLocations.Load(zone); ok {
		loc, _ := cached.(*time.Location)

		return loc, nil
	}

	if ambiguousZoneAbbrevs[zone] {
		return nil, fmt.Errorf("%w: the time zone %q is ambiguous", ErrDatetime, zone)
	}

	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return nil, fmt.Errorf("%w: unknown time zone %q", ErrDatetime, zone)
	}

	zoneLocations.Store(zone, loc)

	return loc, nil
}

// scanDatetime splits the text of a datetime into its parts: the date, then
// the time and the time zone, either of which may be left out.
func scanDatetime(text string) (datetimeParts, bool) {
	var parts datetimeParts

	scan := scanner{text: text, pos: 0}

	if !scan.date(&parts) {
		return parts, false
	}

	if scan.skip('T') || scan.skip('t') || (scan.peekAt(1, isDigit) && scan.skip(' ')) {
		parts.hasTime = true

		if !scan.clock(&parts) {
			return parts, false
		}
	}

	if scan.done() {
		return parts, true
	}

	parts.hasZone = true

	return parts, scan.zone(&parts)
}

// scanner reads a text from pos on.
type scanner struct {
	text string
	pos  int
}

func (s *scanner) done() bool { return s.pos == len(s.text) }

func (s *scanner) rest() string { return s.text[s.pos:] }

func (s *scanner) peek(char byte) bool {
	return s.pos < len(s.text) && s.text[s.pos] == char
}

// peekAt reports whether the byte offset bytes ahead is one match accepts.
func (s *scanner) peekAt(offset int, match func(byte) bool) bool {
	return s.pos+offset < len(s.text) && match(s.text[s.pos+offset])
}

func (s *scanner) skip(char byte) bool {
	if !s.peek(char) {
		return false
	}

	s.pos++

	return true
}

func isDigit(char byte) bool { return char >= '0' && char <= '9' }

func isUpper(char byte) bool { return char >= 'A' && char <= 'Z' }

// date reads a date, YYYY-MM-DD, with a sign before a negative year.
func (s *scanner) date(parts *datetimeParts) bool {
	sign, least := 1, yearDigits
	if s.skip('-') {
		sign, least = -1, negativeYearDigits
	} else {
		s.skip('+')
	}

	year, ok := s.digits(least, maxYearDigits)
	if !ok || !s.skip('-') {
		return false
	}

	parts.year = sign * year

	if parts.month, ok = s.number(1, maxMonth); !ok || !s.skip('-') {
		return false
	}

	parts.day, ok = s.number(1, maxDay)

	return ok
}

// clock reads a time: HH:MM, HH:MM:SS, or HH:MM:SS with a fraction after a
// point or a comma.
func (s *scanner) clock(parts *datetimeParts) bool {
	var ok bool

	if parts.hour, ok = s.number(0, maxHour); !ok || !s.skip(':') {
		return false
	}

	if parts.minute, ok = s.number(0, maxMinute); !ok {
		return false
	}

	if !s.skip(':') {
		return true
	}

	if parts.second, ok = s.number(0, maxSecond); !ok {
		return false
	}

	if s.skip('.') || s.skip(',') {
		parts.nsec, ok = s.fraction()
	}

	return ok
}

// zone reads the rest of the text as a time zone: Z, an offset, a name after
// a space, an offset and a name after a space, or a military one-letter
// name.
func (s *scanner) zone(parts *datetimeParts) bool {
	switch {
	case s.skip('Z') || s.skip('z'):
		return s.done()
	case s.peek('+') || s.peek('-'):
		offset, ok := s.offset()
		if !ok {
			return false
		}

		parts.offset = offset

		if s.skip(' ') {
			parts.hasOffset = true

			return s.zoneName(parts)
		}

		return s.done()
	case s.skip(' '):
		return s.zoneName(parts)
	case parts.hasTime && len(s.rest()) == 1 && s.peekAt(0, isUpper):
		parts.zone = s.rest()

		return true
	}

	return false
}

// zoneName reads the rest of the text as the name of a time zone.
func (s *scanner) zoneName(parts *datetimeParts) bool {
	parts.zone = s.rest()

	return parts.zone != "" && !strings.ContainsAny(parts.zone, " \t")
}

// digits reads a number of no fewer digits than least, and of no more than
// most unless most is 0.
func (s *scanner) digits(least, most int) (int, bool) {
	start := s.pos
	for s.pos < len(s.text) && isDigit(s.text[s.pos]) && (most == 0 || s.pos-start < most) {
		s.pos++
	}

	if s.pos-start < least {
		return 0, false
	}

	value, err := strconv.Atoi(s.text[start:s.pos])

	return value, err == nil
}

// skipDigits moves past the digits at pos and reports whether there are any.
func (s *scanner) skipDigits() bool {
	start := s.pos
	for s.pos < len(s.text) && isDigit(s.text[s.pos]) {
		s.pos++
	}

	return s.pos > start
}

// number reads a part of a datetime, two digits, from low to high.
func (s *scanner) number(low, high int) (int, bool) {
	value, ok := s.digits(partDigits, partDigits)

	return value, ok && value >= low && value <= high
}

// fraction reads up to nine digits of a fraction of a second as nanoseconds.
func (s *scanner) fraction() (int, bool) {
	start := s.pos

	value, ok := s.digits(1, nsecDigits)
	if !ok {
		return 0, false
	}

	for range nsecDigits - (s.pos - start) {
		value *= decimalBase
	}

	return value, true
}

// offset reads an offset from UTC, ±HH, ±HHMM or ±HH:MM, in seconds.
func (s *scanner) offset() (int, bool) {
	sign := 1
	if s.skip('-') {
		sign = -1
	} else {
		s.skip('+')
	}

	hours, ok := s.number(0, maxHour)
	if !ok {
		return 0, false
	}

	minutes := 0
	if s.skip(':') || s.peekAt(0, isDigit) {
		if minutes, ok = s.number(0, maxMinute); !ok {
			return 0, false
		}
	}

	return sign * (hours*minutesPerHour + minutes) * secondsPerMin, true
}

// ParseInterval parses the text of an interval [Text] writes: the units that
// are not zero, each a signed integer and its name - years, months, weeks,
// days, hours, minutes, seconds, nanoseconds, or the singular of one -
// separated by ", ", and its adjust mode, as in "+1 years, -2 seconds, adjust
// last". It reads as well the nine integers separated by commas
// github.com/tarantool/go-tupleconv writes: the eight units in that order and
// the adjust mode as go-tarantool numbers it.
func ParseInterval(text string) (datetime.Interval, error) {
	if ival, ok := parseLegacyInterval(text); ok {
		return ival, nil
	}

	var (
		ival   datetime.Interval
		seen   [len(intervalUnits) + 1]bool
		fields = [len(intervalUnits)]*int64{
			&ival.Year, &ival.Month, &ival.Week, &ival.Day,
			&ival.Hour, &ival.Min, &ival.Sec, &ival.Nsec,
		}
	)

	for part := range strings.SplitSeq(text, ", ") {
		number, unit, ok := strings.Cut(part, " ")
		if !ok {
			return datetime.Interval{}, fmt.Errorf("%q: %w", text, ErrInterval)
		}

		if number == intervalAdjust {
			adjust, ok := adjustByName(unit)
			if !ok || seen[len(intervalUnits)] {
				return datetime.Interval{}, fmt.Errorf("%q: %w", text, ErrInterval)
			}

			seen[len(intervalUnits)] = true
			ival.Adjust = adjust

			continue
		}

		index := intervalUnitIndex(unit)

		value, err := strconv.ParseInt(number, decimalBase, 64)
		if index < 0 || err != nil || seen[index] {
			return datetime.Interval{}, fmt.Errorf("%q: %w", text, ErrInterval)
		}

		seen[index] = true
		*fields[index] = value
	}

	return ival, nil
}

// intervalUnitIndex is the index of a unit, named in plural or singular, in
// intervalUnits, or -1.
func intervalUnitIndex(unit string) int {
	for index, name := range intervalUnits {
		if unit == name || unit == strings.TrimSuffix(name, "s") {
			return index
		}
	}

	return -1
}

// adjustByName is the adjust mode Tarantool names name, or the one of a mode
// it has no name for, written as its number as intervalText writes it.
func adjustByName(name string) (datetime.Adjust, bool) {
	for adjust, known := range adjustNames {
		if known == name {
			return adjust, true
		}
	}

	number, err := strconv.Atoi(name)
	if err != nil || number < 0 {
		return datetime.NoneAdjust, false
	}

	return datetime.Adjust(number), true
}

// parseLegacyInterval parses the text of an interval go-tupleconv writes.
func parseLegacyInterval(text string) (datetime.Interval, bool) {
	parts := strings.Split(text, ",")
	if len(parts) != legacyIntervalFields {
		return datetime.Interval{}, false
	}

	var values [legacyIntervalFields]int64

	for index, part := range parts {
		value, err := strconv.ParseInt(part, decimalBase, 64)
		if err != nil {
			return datetime.Interval{}, false
		}

		values[index] = value
	}

	adjust := datetime.Adjust(values[8])
	if _, ok := adjustNames[adjust]; !ok {
		return datetime.Interval{}, false
	}

	return datetime.Interval{
		Year: values[0], Month: values[1], Week: values[2], Day: values[3],
		Hour: values[4], Min: values[5], Sec: values[6], Nsec: values[7],
		Adjust: adjust,
	}, true
}
