package search

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/install_ee"
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
	left := []uint64{
		verLeft.Major, verLeft.Minor,
		verLeft.Patch, uint64(verLeft.Release.Type),
		verLeft.Release.Num, verLeft.Additional, verLeft.Revision,
	}
	right := []uint64{
		verRight.Major, verRight.Minor,
		verRight.Patch, uint64(verRight.Release.Type),
		verRight.Release.Num, verRight.Additional, verRight.Revision,
	}

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

// compileVersionRegexp compiles a regular expression for cutting version from SDK bundle names.
func compileVersionRegexp(prg string) (*regexp.Regexp, error) {
	var expr string

	switch prg {
	case ProgramEe:
		expr = "^(?P<tarball>tarantool-enterprise-sdk-(?P<version>.*r[0-9]{1,3}).*\\.tar\\.gz)$"
	case ProgramTcm:
		expr = `^(?P<tarball>tcm-(?P<version>\d+\.\d+\.\d+[^.]*).*\.tar\.gz)$`
	default:
		return nil, fmt.Errorf("unknown version format for program: %q", prg)
	}

	re := regexp.MustCompile(expr)
	return re, nil
}

// getBundles collects a list of information about all available tarantool-ee
// bundles from tarantool.io api reply.
func getBundles(rawBundleInfoList map[string][]string, searchCtx *SearchCtx) (
	BundleInfoSlice, error,
) {
	token := ""
	if searchCtx.tntIoDoer != nil {
		token = searchCtx.tntIoDoer.Token()
	}

	bundles := BundleInfoSlice{}

	re, err := compileVersionRegexp(searchCtx.ProgramName)
	if err != nil {
		return nil, err
	}

	for release, pkgs := range rawBundleInfoList {
		for _, pkg := range pkgs {
			parsedData := util.FindNamedMatches(re, pkg)
			if len(parsedData) == 0 {
				continue
			}

			version, err := version.Parse(parsedData["version"])
			if err != nil {
				return nil, err
			}

			version.Tarball = pkg
			eeVer := BundleInfo{
				Version: version,
				Package: "enterprise",
				Release: release,
				Token:   token,
			}

			switch searchCtx.Filter {
			case SearchRelease:
				if strings.Contains(pkg, "-debug-") {
					continue
				}
			case SearchDebug:
				if !strings.Contains(pkg, "-debug-") {
					continue
				}
			}

			bundles = append(bundles, eeVer)
		}
	}

	if len(bundles) == 0 {
		return nil, fmt.Errorf("no packages found for this OS or release version")
	}

	sort.Sort(bundles)

	return bundles, nil
}

// fetchBundlesInfo returns slice of information about all available tarantool-ee bundles.
// The result will be sorted in ascending order.
func fetchBundlesInfo(searchCtx *SearchCtx, cliOpts *config.CliOpts) (
	BundleInfoSlice, error,
) {
	credentials, err := install_ee.GetCreds(cliOpts)
	if err != nil {
		return nil, err
	}

	ref, err := tntIoGetPkgVersions(credentials, searchCtx)
	if err != nil {
		return nil, err
	}

	bundles, err := getBundles(ref, searchCtx)
	if err != nil {
		return nil, err
	}

	return bundles, nil
}
