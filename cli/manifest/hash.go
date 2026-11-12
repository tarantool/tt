package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// This file is manifest_hash: the value a lock records to name the manifest
// it was resolved from, and the check of a manifest against it. The
// application lock and the lock of a distribution share it.
//
// # What the hash covers
//
// manifest_hash is taken over what the manifest says, not over how the file
// says it. The document is decoded into the generic tree go-toml decodes into
// when it has no struct to fill - tables, arrays and scalars - the tree is
// encoded into the canonical form below, and the hash is the SHA-256 of that.
// Comments, blank lines and indentation, the order of keys and of tables, the
// quoting of a key or a string (basic, literal, multi-line, escaped), the form
// of a table (standard, inline, dotted keys) and the spelling of a number
// (0x10 is 16, 1_000 is 1000, 1e2 is 100.0) do not reach it. A changed key,
// value, type of a value or order of an array does: a lock goes stale for an
// edit that can change what tt resolves or builds, and for no other.
//
// The tree is the generic one and not the Manifest struct on purpose: a tt
// that adds a field, or a default, to the struct would change the hash of
// every manifest it reads and make every lock stale at once, while the generic
// tree holds what the file holds and nothing else. It follows that an empty
// table is a value: [dependencies] with nothing under it is not the same as no
// [dependencies].
//
// # The canonical form, version 1
//
// A value is a one-byte tag and a payload. The numbers the form adds, lengths
// and counts, are uint64, big-endian.
//
//	tag  TOML type         payload
//	t    table             count; for each key in ascending byte order: the
//	                       length and the bytes of the key, then its value
//	a    array             count; each element, in order
//	s    string            length; the bytes of the decoded string
//	i    integer           int64, two's complement, big-endian
//	f    float             IEEE 754 binary64 bits, big-endian
//	b    boolean           one byte, 0 or 1
//	o    offset date-time  length; YYYY-MM-DDTHH:MM:SS[.F]Z or [.F]+HH:MM/-HH:MM
//	l    local date-time   length; YYYY-MM-DDTHH:MM:SS[.F]
//	d    local date        length; YYYY-MM-DD
//	c    local time        length; HH:MM:SS[.F]
//
// The document is its root table. Keys and strings are their decoded UTF-8,
// compared and hashed as bytes, with no Unicode normalization; Go orders
// strings by their bytes. An array keeps its order, which is meaning in TOML.
// Every payload has a fixed length or starts with its length or count, so the
// form is a prefix code: a byte string decodes into one tree at most, and two
// different trees never encode alike - neither two values of different types
// (1 and 1.0, "1" and 1) nor a run of values and another value.
//
// A float is its bits: 1e2 and 100.0 are one value; -0.0 and 0.0, which IEEE
// 754 tells apart, are two; inf and -inf are their own bits; every NaN, of
// whatever sign or payload, is 0x7ff8000000000001, which Go's math.NaN() is.
//
// The tags keep apart the four kinds of date and time TOML keeps apart. Within
// a kind, the text writes a value one way only: the separator of date and time
// is T; the fraction of a second, [.F], is its nanoseconds with the trailing
// zeros cut and is left out when they are all zero, so 07:32:00.5 and
// 07:32:00.500 are one value; an offset of zero is Z, so Z, +00:00 and -00:00
// are one. An offset date-time keeps its offset: 1979-05-27T07:32:00Z and
// 1979-05-27T00:32:00-07:00 are one instant but two values, as they are to
// go-toml, which keeps the offset in the time.Time it decodes, and to anything
// that prints one. Keeping them apart costs a resolve at most; taking them
// together would hide an edit of the offset from the lock. No field of a
// manifest is a date-time today.
//
// A value of a Go type the form has no tag for is an error, never skipped: a
// go-toml that decoded some TOML type into another Go type would fail every
// manifest holding one, rather than hash it as if it were not there.
//
// # The value
//
// manifest_hash is the name of the scheme, a colon and the hash in lowercase
// hex: "toml1-sha256:<hex>" for the scheme above. Another form or another hash
// function is a scheme with another name, never a new meaning of this one, so
// a lock always says how its hash was made, and a hash is only ever compared
// with one made the same way.
//
// A lock of any other scheme is out of date. tt 3.0.0-alpha.2 and older wrote
// "sha256:<hex>", the SHA-256 of the bytes of the file: such a lock, and one
// whose value names no scheme at all, is resolved again, and a --locked
// build says that tt hashes the manifest differently now. It is not compared
// the way the older tt compared it, on purpose: a lock that stayed current
// that way would go stale on the first comment, and the rule above would
// hold for some locks and not for others.

const (
	// hashScheme starts the manifest_hash tt writes: the SHA-256 of the
	// canonical form, version 1.
	hashScheme = "toml1-sha256:"
	// hashSchemeBytes starts the manifest_hash of tt 3.0.0-alpha.2 and
	// older: the SHA-256 of the bytes of the file.
	hashSchemeBytes = "sha256:"
)

// The tags of the canonical form.
const (
	tagTable          = 't'
	tagArray          = 'a'
	tagString         = 's'
	tagInteger        = 'i'
	tagFloat          = 'f'
	tagBool           = 'b'
	tagOffsetDateTime = 'o'
	tagLocalDateTime  = 'l'
	tagLocalDate      = 'd'
	tagLocalTime      = 'c'
)

// canonicalNaN is the bits of every NaN in the canonical form, math.NaN().
const canonicalNaN = 0x7ff8000000000001

var (
	// ErrManifestChanged reports a manifest other than the one a lock was
	// resolved from.
	ErrManifestChanged = errors.New("manifest changed since the lock was written")
	// ErrHashOutdated reports a manifest_hash of the scheme of an older tt,
	// or of no scheme at all.
	ErrHashOutdated = errors.New("manifest_hash of the lock is of an older tt; " +
		"tt hashes the manifest differently now")
	// ErrHashScheme reports a manifest_hash of a scheme this tt does not
	// know: a newer tt wrote it, or a hand did.
	ErrHashScheme = errors.New("manifest_hash of the lock is of a scheme this tt does not know")

	// errHashType reports a value of a Go type the canonical form has no tag
	// for.
	errHashType = errors.New("no canonical form for the value")
	// errHashOffset reports an offset date-time whose offset is not a whole
	// number of minutes, which TOML cannot write.
	errHashOffset = errors.New("offset of a date-time is not whole minutes")
)

// HashTOML returns the manifest_hash of a TOML document: "toml1-sha256:" and
// the SHA-256 of the canonical form of its tree, in hex. See the top of this
// file for the form.
func HashTOML(data []byte) (string, error) {
	var tree map[string]any

	err := toml.Unmarshal(data, &tree)
	if err != nil {
		return "", fmt.Errorf("parsing manifest: %w", err)
	}

	form, err := canonicalForm(tree)
	if err != nil {
		return "", fmt.Errorf("hashing manifest: %w", err)
	}

	sum := sha256.Sum256(form)

	return hashScheme + hex.EncodeToString(sum[:]), nil
}

// checkHash compares recorded, the manifest_hash of a lock, with current, the
// hash of a manifest: nil when the lock was written for the manifest, an
// error saying why not otherwise.
func checkHash(recorded, current string) error {
	scheme, _, named := strings.Cut(recorded, ":")

	switch {
	case strings.HasPrefix(recorded, hashScheme):
		if recorded != current {
			return ErrManifestChanged
		}

		return nil
	case !named || strings.HasPrefix(recorded, hashSchemeBytes):
		return ErrHashOutdated
	default:
		return fmt.Errorf("%w: %q", ErrHashScheme, scheme)
	}
}

// canonicalForm encodes the tree of a document, its root table, in the
// canonical form.
func canonicalForm(tree map[string]any) ([]byte, error) {
	var form canonical

	err := form.value("", tree)
	if err != nil {
		return nil, err
	}

	return form.buf, nil
}

// canonical is the canonical form of a tree as it is encoded.
type canonical struct {
	buf []byte
}

// value appends a value. path is the dotted key of the value, for an error.
func (c *canonical) value(path string, value any) error {
	switch value := value.(type) {
	case map[string]any:
		return c.table(path, value)
	case []any:
		return c.array(path, value)
	case string:
		c.text(tagString, value)
	case int64:
		c.buf = append(c.buf, tagInteger)
		c.buf = binary.BigEndian.AppendUint64(c.buf,
			uint64(value)) //nolint:gosec // Two's complement is the encoding.
	case float64:
		bits := math.Float64bits(value)
		if math.IsNaN(value) {
			bits = canonicalNaN
		}

		c.buf = append(c.buf, tagFloat)
		c.buf = binary.BigEndian.AppendUint64(c.buf, bits)
	case bool:
		c.buf = append(c.buf, tagBool, boolByte(value))
	case time.Time:
		offset, err := offsetText(value)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		year, month, day := value.Date()
		c.text(tagOffsetDateTime, dateText(year, int(month), day)+"T"+
			clockText(value.Hour(), value.Minute(), value.Second(), value.Nanosecond())+offset)
	case toml.LocalDateTime:
		c.text(tagLocalDateTime, localDateText(value.LocalDate)+"T"+localTimeText(value.LocalTime))
	case toml.LocalDate:
		c.text(tagLocalDate, localDateText(value))
	case toml.LocalTime:
		c.text(tagLocalTime, localTimeText(value))
	default:
		return fmt.Errorf("%w: %s is a %T", errHashType, path, value)
	}

	return nil
}

// table appends a table: its count, then its keys in byte order, each
// followed by its value.
func (c *canonical) table(path string, table map[string]any) error {
	c.buf = append(c.buf, tagTable)
	c.size(len(table))

	for _, key := range slices.Sorted(maps.Keys(table)) {
		c.size(len(key))

		c.buf = append(c.buf, key...)

		child := key
		if path != "" {
			child = path + "." + key
		}

		err := c.value(child, table[key])
		if err != nil {
			return err
		}
	}

	return nil
}

// array appends an array: its count, then its elements in order.
func (c *canonical) array(path string, array []any) error {
	c.buf = append(c.buf, tagArray)
	c.size(len(array))

	for i, element := range array {
		err := c.value(path+"["+strconv.Itoa(i)+"]", element)
		if err != nil {
			return err
		}
	}

	return nil
}

// text appends a tag and a length-prefixed text.
func (c *canonical) text(tag byte, text string) {
	c.buf = append(c.buf, tag)
	c.size(len(text))

	c.buf = append(c.buf, text...)
}

// size appends a length or a count.
func (c *canonical) size(n int) {
	c.buf = binary.BigEndian.AppendUint64(c.buf,
		uint64(n)) //nolint:gosec // A length is never negative.
}

// boolByte is a boolean in the canonical form.
func boolByte(value bool) byte {
	if value {
		return 1
	}

	return 0
}

// localDateText renders a local date as YYYY-MM-DD.
func localDateText(date toml.LocalDate) string {
	return dateText(date.Year, date.Month, date.Day)
}

// localTimeText renders a local time as HH:MM:SS[.F]. The precision the
// document wrote the fraction with is not part of the value.
func localTimeText(clock toml.LocalTime) string {
	return clockText(clock.Hour, clock.Minute, clock.Second, clock.Nanosecond)
}

// dateText renders a date as YYYY-MM-DD.
func dateText(year, month, day int) string {
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// clockText renders a time of day as HH:MM:SS, followed by the fraction of
// the second with the trailing zeros cut, when there is one.
func clockText(hour, minute, second, nanosecond int) string {
	text := fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
	if nanosecond == 0 {
		return text
	}

	return text + "." + strings.TrimRight(fmt.Sprintf("%09d", nanosecond), "0")
}

// offsetText renders the offset of a date-time: Z for none, +HH:MM or -HH:MM
// otherwise.
func offsetText(value time.Time) (string, error) {
	_, seconds := value.Zone()

	offset := time.Duration(seconds) * time.Second
	if offset == 0 {
		return "Z", nil
	}

	if offset%time.Minute != 0 {
		return "", fmt.Errorf("%w: %s", errHashOffset, offset)
	}

	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}

	return fmt.Sprintf("%s%02d:%02d", sign,
		int64(offset/time.Hour), int64(offset%time.Hour/time.Minute)), nil
}
