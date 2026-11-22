package rws

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

var (
	// upstreamVersionPattern is a version of Tarantool 3 the source lists.
	upstreamVersionPattern = regexp.MustCompile(`^3\.[0-9]+\.[0-9]+(?:[-.][A-Za-z0-9.]+)?$`)
	// revisionPattern is the Debian revision of a package.
	revisionPattern = regexp.MustCompile(`^[A-Za-z0-9+.~]+$`)
)

// debVersion is the version of a Debian package, [epoch:]upstream[-revision]:
// "3.9.0-1" is the revision 1 of Tarantool 3.9.0.
type debVersion struct {
	// raw is the version as the index spells it.
	raw string
	// epoch is 0 when the version has none.
	epoch    uint64
	upstream string
	// revision is empty for a native package.
	revision string
}

// parseDebVersion parses the version of a package of Tarantool 3: one whose
// upstream version is a release of it. The version of a false result is
// what was parsed of it.
func parseDebVersion(raw string) (debVersion, bool) {
	version := debVersion{raw: raw, epoch: 0, upstream: raw, revision: ""}

	if epoch, rest, found := strings.Cut(raw, ":"); found {
		number, err := strconv.ParseUint(epoch, 10, 32)
		if err != nil {
			return version, false
		}

		version.epoch, version.upstream = number, rest
	}

	// The upstream version has a dash only if there is a revision after it.
	if upstream, revision, found := strings.CutLast(version.upstream, "-"); found {
		if !revisionPattern.MatchString(revision) {
			return version, false
		}

		version.upstream, version.revision = upstream, revision
	}

	return version, upstreamVersionPattern.MatchString(version.upstream)
}

// compareDebVersions orders versions as dpkg does: by the epoch, then the
// upstream version, then the revision.
func compareDebVersions(left, right debVersion) int {
	if comparison := cmp.Compare(left.epoch, right.epoch); comparison != 0 {
		return comparison
	}

	if comparison := compareVersionParts(left.upstream, right.upstream); comparison != 0 {
		return comparison
	}

	return compareVersionParts(left.revision, right.revision)
}

// compareVersionParts compares an upstream version or a revision as dpkg
// does: the runs of digits by their numeric value, the other runs character
// by character, a tilde before anything, the end, letters, and other
// characters last. So "1~rc1" < "1" < "1a" < "1+b1" and "1.9" < "1.10".
func compareVersionParts(left, right string) int {
	leftIndex, rightIndex := 0, 0

	for leftIndex < len(left) || rightIndex < len(right) {
		for leftIndex < len(left) && !isDigit(left[leftIndex]) ||
			rightIndex < len(right) && !isDigit(right[rightIndex]) {
			leftOrder, rightOrder := versionOrder(left, leftIndex), versionOrder(right, rightIndex)
			if leftOrder != rightOrder {
				return cmp.Compare(leftOrder, rightOrder)
			}

			leftIndex, rightIndex = leftIndex+1, rightIndex+1
		}

		for leftIndex < len(left) && left[leftIndex] == '0' {
			leftIndex++
		}

		for rightIndex < len(right) && right[rightIndex] == '0' {
			rightIndex++
		}

		firstDifference := 0

		for leftIndex < len(left) && isDigit(left[leftIndex]) &&
			rightIndex < len(right) && isDigit(right[rightIndex]) {
			if firstDifference == 0 {
				firstDifference = cmp.Compare(left[leftIndex], right[rightIndex])
			}

			leftIndex, rightIndex = leftIndex+1, rightIndex+1
		}

		// The longer run of digits, without leading zeros, is the larger one.
		if leftIndex < len(left) && isDigit(left[leftIndex]) {
			return 1
		}

		if rightIndex < len(right) && isDigit(right[rightIndex]) {
			return -1
		}

		if firstDifference != 0 {
			return firstDifference
		}
	}

	return 0
}

// versionOrder is the weight of the character of value at index outside a
// run of digits; past the end of value, and on a digit, it is 0.
func versionOrder(value string, index int) int {
	if index >= len(value) {
		return 0
	}

	character := value[index]

	switch {
	case isDigit(character):
		return 0
	case character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z':
		return int(character)
	case character == '~':
		return -1
	default:
		return int(character) + 1<<8
	}
}

func isDigit(character byte) bool {
	return character >= '0' && character <= '9'
}
