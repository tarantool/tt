package search

import (
	"fmt"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/version"
)

// getPackageName returns the package name at tarantool.io for the given program.
func getPackageName(program string) string {
	switch program {
	case ProgramEe:
		return "enterprise"
	case ProgramTcm:
		return "tarantool-cluster-manager"
	}
	return ""
}

// searchVersionsTntIo handles searching versions using the tarantool.io customer zone.
func searchVersionsTntIo(cliOpts *config.CliOpts, searchCtx *SearchCtx) (
	version.VersionSlice, error,
) {
	searchCtx.Package = getPackageName(searchCtx.ProgramName)
	if searchCtx.Package == "" {
		return nil, fmt.Errorf("there is no tarantool.io package for program: %s",
			searchCtx.ProgramName)
	}

	bundles, err := fetchBundlesInfo(searchCtx, cliOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bundle info for %s: %w",
			searchCtx.ProgramName, err)
	}

	if len(bundles) == 0 {
		return nil, fmt.Errorf("no versions found for %s matching the criteria",
			searchCtx.ProgramName)
	}

	vers := make(version.VersionSlice, bundles.Len())
	for i, bundle := range bundles {
		vers[i] = bundle.Version
	}
	return vers, nil
}

func getSdkBundleInfoRemote(cliOpts *config.CliOpts, devBuild bool, expectedVersion string) (
	BundleInfo, error,
) {
	// FIXME: Use correct search context. https://jira.vk.team/browse/TNTP-1095
	searchCtx := SearchCtx{
		ProgramName:      ProgramEe,
		Filter:           SearchAll,
		Package:          "enterprise",
		DevBuilds:        devBuild,
		platformInformer: NewPlatformInformer(),
		tntIoDoer:        NewTntIoDoer(),
	}
	bundles, err := fetchBundlesInfo(&searchCtx, cliOpts)
	if err != nil {
		return BundleInfo{}, err
	}
	for _, bundle := range bundles {
		if bundle.Version.Str == expectedVersion {
			return bundle, nil
		}
	}
	return BundleInfo{}, fmt.Errorf("expected version %s not found", expectedVersion)
}

// GetEeBundleInfo returns the available EE SDK bundle for user's OS,
// corresponding to the passed expected version argument.
func GetEeBundleInfo(cliOpts *config.CliOpts, local bool, devBuild bool,
	files []string, expectedVersion string,
) (BundleInfo, error) {
	if local {
		return getSdkBundleInfoLocal(files, expectedVersion)
	}
	return getSdkBundleInfoRemote(cliOpts, devBuild, expectedVersion)
}
