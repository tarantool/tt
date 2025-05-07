package search

import (
	"fmt"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/version"
)

// GetApiPackage returns the package name at tarantool.io for the given program.
func GetApiPackage(program ProgramType) string {
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
	bundles, err := FetchBundlesInfo(searchCtx, cliOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bundle info for %s: %w",
			searchCtx.Program, err)
	}

	if len(bundles) == 0 {
		return nil, fmt.Errorf("no versions found for %s matching the criteria",
			searchCtx.Program)
	}

	vers := make(version.VersionSlice, bundles.Len())
	for i, bundle := range bundles {
		vers[i] = bundle.Version
	}
	return vers, nil
}
