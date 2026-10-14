package search

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errNoVersionsFoundForMatchingTheCriteria    = errors.New("no versions found for ")
	errNoAvailableVersions                      = errors.New("no available versions")
	errNoPackagesFoundForThisOSOrReleaseVersion = errors.New(
		"no packages found for this OS or release version",
	)
	errTarantoolIOPackageNotFound = errors.New(
		"there is no tarantool.io package for program: ",
	)
	errUnknownVersionFormatForProgram = errors.New("unknown version format for program: ")
	errVersionNotFound                = errors.New(" version doesn't found")
)

// BundleInfo contains the version and download information of an archive.
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

// Swap implements sort.Interface.
func (bundles BundleInfoSlice) Swap(i, j int) {
	bundles[i], bundles[j] = bundles[j], bundles[i]
}

// Len implements sort.Interface.
func (bundles BundleInfoSlice) Len() int {
	return len(bundles)
}

// Less implements sort.Interface.
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

	largestLen := binary.Max(len(left), len(right))

	for idx := range largestLen {
		var valLeft, valRight uint64 = 0, 0

		if idx < len(left) {
			valLeft = left[idx]
		}

		if idx < len(right) {
			valRight = right[idx]
		}

		if valLeft != valRight {
			return valLeft < valRight
		}
	}

	return false
}

// compileVersionRegexp extracts versions from SDK or standalone archive names.
func compileVersionRegexp(prg binary.Program) (*regexp.Regexp, error) {
	var expr string

	source := prg.BundleSource()
	switch {
	case source == binary.ProgramSDK:
		expr = "^(?P<tarball>tarantool-enterprise-sdk-(?P<version>.*r[0-9]{1,3}).*\\.tar\\.gz)$"
	case source.IsStandaloneArchive():
		expr = `^(?P<tarball>` + regexp.QuoteMeta(source.String()) +
			`-(?P<version>\d+\.\d+\.\d+[^.]*?)` +
			`(?:[.-](?:linux|macos|darwin)[.-](?:amd64|arm64|x86_64|aarch64))?\.tar\.gz)$`
	default:
		return nil, fmt.Errorf("%w%q", errUnknownVersionFormatForProgram, prg)
	}

	return regexp.MustCompile(expr), nil
}

// getBundles collects available product archives from the tarantool.io API reply.
func getBundles(rawBundleInfoList map[string][]string, searchCtx *SearchCtx) (
	BundleInfoSlice, error,
) {
	token := ""
	if searchCtx.TntIoDoer != nil {
		token = searchCtx.TntIoDoer.Token()
	}

	bundles := BundleInfoSlice{}

	versionRegexp, err := compileVersionRegexp(searchCtx.Program)
	if err != nil {
		return nil, err
	}

	platformSuffix, err := bundlePlatformSuffix(searchCtx.Program, searchCtx.platformInformer)
	if err != nil {
		return nil, err
	}

	for release, pkgs := range rawBundleInfoList {
		for _, pkg := range pkgs {
			if !strings.HasSuffix(pkg, platformSuffix) {
				continue
			}

			parsedData := binary.FindNamedMatches(versionRegexp, pkg)
			if len(parsedData) == 0 {
				continue
			}

			version, err := version.Parse(parsedData["version"])
			if err != nil {
				log.Debugf(
					"failed to parse version of %s from package %s: %v",
					searchCtx.Program,
					pkg,
					err,
				)

				continue
			}

			if !matchesReleaseFilter(searchCtx, version) {
				continue
			}

			version.Tarball = pkg

			eeVer := BundleInfo{
				Version: version,
				Package: searchCtx.Package,
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
			case SearchAll:
				// Keep both release and debug packages.
			}

			bundles = append(bundles, eeVer)
		}
	}

	if len(bundles) == 0 {
		return nil, errNoPackagesFoundForThisOSOrReleaseVersion
	}

	sort.Sort(bundles)

	return bundles, nil
}

// matchesReleaseFilter applies --version to the flat tt-ee catalog; the other
// sources filter versions through their API directory hierarchy.
func matchesReleaseFilter(searchCtx *SearchCtx, available version.Version) bool {
	if searchCtx.Program != binary.ProgramTtEe || searchCtx.ReleaseVersion == "" {
		return true
	}

	_, err := version.MatchVersion(searchCtx.ReleaseVersion, []version.Version{available})

	return err == nil
}

// FetchBundlesInfo returns available product archives in ascending version order.
func FetchBundlesInfo(searchCtx *SearchCtx) (
	BundleInfoSlice, error,
) {
	searchCtx.Package = GetAPIPackage(searchCtx.Program)
	if searchCtx.Package == "" {
		return nil, fmt.Errorf("%w%s",
			errTarantoolIOPackageNotFound, searchCtx.Program)
	}

	_, err := bundlePlatformSuffix(searchCtx.Program, searchCtx.platformInformer)
	if err != nil {
		return nil, err
	}

	credentials, err := connect.GetCreds(searchCtx.CredentialsFile)
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

// SelectVersion selects a specific version from the list of available bundles.
// If no version is specified, it returns the latest version.
func SelectVersion(bundles BundleInfoSlice, ver string) (BundleInfo, error) {
	if bundles == nil || bundles.Len() == 0 {
		return BundleInfo{}, errNoAvailableVersions
	}

	if ver == "" {
		// No version specified, return the latest one.
		return bundles[bundles.Len()-1], nil
	}

	for _, bundle := range bundles {
		if bundle.Version.Str == ver {
			return bundle, nil
		}
	}

	return BundleInfo{}, fmt.Errorf("%q%w", ver, errVersionNotFound)
}

// GetAPIPackage returns the package name at tarantool.io for the given program.
func GetAPIPackage(program binary.Program) string {
	switch program.BundleSource() {
	case binary.ProgramSDK:
		return "enterprise"
	case binary.ProgramTcm:
		return "tarantool-cluster-manager"
	case binary.ProgramTtEe:
		return binary.ProgramTtEe.String()
	default:
		// These programs do not have a customer-zone API package name.
	}

	return ""
}

// searchVersionsTntIo handles searching versions using the tarantool.io customer zone.
func searchVersionsTntIo(searchCtx *SearchCtx) (
	version.VersionSlice, error,
) {
	bundles, err := FetchBundlesInfo(searchCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bundle info for %s: %w",
			searchCtx.Program, err)
	}

	if len(bundles) == 0 {
		return nil, fmt.Errorf("%w%s matching the criteria",
			errNoVersionsFoundForMatchingTheCriteria, searchCtx.Program)
	}

	vers := make(version.VersionSlice, bundles.Len())
	for i, bundle := range bundles {
		vers[i] = bundle.Version
	}

	return vers, nil
}
