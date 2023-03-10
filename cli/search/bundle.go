package search

import (
	"github.com/tarantool/tt/cli/version"
)

// BundleInfo is a structure that contains specific information about SDK bundle.
type BundleInfo struct {
	// Version represents the info about the bundle's version.
	Version version.Version
	// Prefix represents the relative URL of the bundle.
	Prefix string
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
	return version.Less(verLeft, verRight)
}
