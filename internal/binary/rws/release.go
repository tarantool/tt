package rws

import (
	"strings"
)

// comparePackageReleases compares RPM release segments: numbers compare by value,
// numeric segments sort after letters, and ~ sorts before a normal release.
func comparePackageReleases(left, right string) int {
	for left != "" || right != "" {
		left = strings.TrimLeftFunc(left, isPackageReleaseSeparator)
		right = strings.TrimLeftFunc(right, isPackageReleaseSeparator)

		if strings.HasPrefix(left, "~") || strings.HasPrefix(right, "~") {
			if !strings.HasPrefix(left, "~") {
				return 1
			}

			if !strings.HasPrefix(right, "~") {
				return -1
			}

			left, right = left[1:], right[1:]

			continue
		}

		if strings.HasPrefix(left, "^") || strings.HasPrefix(right, "^") {
			switch {
			case left == "":
				return -1
			case right == "":
				return 1
			case left[0] != '^':
				return 1
			case right[0] != '^':
				return -1
			}

			left, right = left[1:], right[1:]

			continue
		}

		if left == "" || right == "" {
			return strings.Compare(left, right)
		}

		leftNumber, rightNumber := isPackageReleaseDigit(left[0]), isPackageReleaseDigit(right[0])
		if leftNumber != rightNumber {
			if leftNumber {
				return 1
			}

			return -1
		}

		leftEnd, rightEnd := packageReleaseSegmentEnd(left), packageReleaseSegmentEnd(right)
		leftSegment, rightSegment := left[:leftEnd], right[:rightEnd]

		if leftNumber {
			leftSegment = strings.TrimLeft(leftSegment, "0")
			rightSegment = strings.TrimLeft(rightSegment, "0")

			if len(leftSegment) > len(rightSegment) {
				return 1
			}

			if len(leftSegment) < len(rightSegment) {
				return -1
			}
		}

		if comparison := strings.Compare(leftSegment, rightSegment); comparison != 0 {
			return comparison
		}

		left, right = left[leftEnd:], right[rightEnd:]
	}

	return 0
}

func isPackageReleaseDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func isPackageReleaseSeparator(character rune) bool {
	return character != '~' && character != '^' &&
		(character < '0' || character > '9') &&
		(character < 'a' || character > 'z') && (character < 'A' || character > 'Z')
}

func packageReleaseSegmentEnd(value string) int {
	numeric := isPackageReleaseDigit(value[0])
	for index := range len(value) {
		character := value[index]
		if isPackageReleaseSeparator(rune(character)) || character == '~' || character == '^' ||
			isPackageReleaseDigit(character) != numeric {
			return index
		}
	}

	return len(value)
}
