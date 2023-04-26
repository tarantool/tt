package search

import (
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// BundleInfo is a structure that contains specific information about SDK bundle.
type BundleInfo struct {
	// Version represents the info about the bundle's version.
	Version version.Version
	// Package represents package name.
	Package string
	// Release represents the release version (e.g: 2.10).
	Release string
	// Download token.
	Token string
}

// BundleInfoSlice attaches the methods of sort.Interface to []Version,
// sorting from oldest to newest.
type BundleInfoSlice []BundleInfo

// sort.Interface Swap implementation
func (bundles BundleInfoSlice) Swap(i, j int) {
	bundles[i], bundles[j] = bundles[j], bundles[i]
}

// sort.Interface Len implementation
func (bundles BundleInfoSlice) Len() int {
	return len(bundles)
}

// sort.Interface Less implementation
func (bundles BundleInfoSlice) Less(i, j int) bool {
	verLeft := bundles[i].Version
	verRight := bundles[j].Version
	return Less(verLeft, verRight)
}

// Less is a common function-comparator using for the Version type.
func Less(verLeft, verRight version.Version) bool {
	left := []uint64{verLeft.Major, verLeft.Minor,
		verLeft.Patch, uint64(verLeft.Release.Type),
		verLeft.Release.Num, verLeft.Additional, verLeft.Revision}
	right := []uint64{verRight.Major, verRight.Minor,
		verRight.Patch, uint64(verRight.Release.Type),
		verRight.Release.Num, verRight.Additional, verRight.Revision}

	largestLen := util.Max(len(left), len(right))

	for i := 0; i < largestLen; i++ {
		var valLeft, valRight uint64 = 0, 0
		if i < len(left) {
			valLeft = left[i]
		}

		if i < len(right) {
			valRight = right[i]
		}

		if valLeft != valRight {
			return valLeft < valRight
		}
	}

	return false
}
