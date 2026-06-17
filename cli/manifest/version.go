package manifest

import (
	"strconv"
	"strings"
)

// versionFieldCount is the number of dot-separated fields in a format version.
const versionFieldCount = 2

// formatVersion is a parsed "<major>.<minor>" format version, used for both
// manifest_version and lock_version.
type formatVersion struct {
	major int
	minor int
}

//nolint:gochecknoglobals // Parsed once from the version constants; effectively constants.
var (
	ourManifestVersion = mustParseFormatVersion(ManifestVersion)
	ourLockVersion     = mustParseFormatVersion(LockVersion)
)

// parseFormatVersion parses exactly two non-negative integers separated by a
// dot. "0.1.0", "0", "x.y" and the like are errors.
func parseFormatVersion(raw string) (formatVersion, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != versionFieldCount {
		return formatVersion{}, invalid("",
			"invalid format version %q: want \"<major>.<minor>\"", raw)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return formatVersion{}, invalid("", "invalid format version %q: bad major", raw)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return formatVersion{}, invalid("", "invalid format version %q: bad minor", raw)
	}

	return formatVersion{major: major, minor: minor}, nil
}

func mustParseFormatVersion(raw string) formatVersion {
	v, err := parseFormatVersion(raw)
	if err != nil {
		panic(err)
	}

	return v
}

func (v formatVersion) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor)
}

type versionRelation int

const (
	relSame       versionRelation = iota // Same major, same minor.
	relMinorNewer                        // Same major, newer minor.
	relMinorOlder                        // Same major, older minor.
	relMajorNewer                        // Newer major.
	relMajorOlder                        // Older major.
)

// relTo classifies v relative to the reference version ref.
func (v formatVersion) relTo(ref formatVersion) versionRelation {
	switch {
	case v.major > ref.major:
		return relMajorNewer
	case v.major < ref.major:
		return relMajorOlder
	case v.minor > ref.minor:
		return relMinorNewer
	case v.minor < ref.minor:
		return relMinorOlder
	default:
		return relSame
	}
}

// minorNewerThan reports whether v has the same major but a strictly newer
// minor than ref - the only case where an unknown field is a warning rather
// than an error.
func (v formatVersion) minorNewerThan(ref formatVersion) bool {
	return v.relTo(ref) == relMinorNewer
}
