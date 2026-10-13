package search

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errNoVersionsFoundForMatchingTheCriteria = errors.New("no versions found for ")
)

// GetAPIPackage returns the package name at tarantool.io for the given program.
func GetAPIPackage(program binary.Program) string {
	switch program {
	case binary.ProgramEe, binary.ProgramSDK:
		return "enterprise"
	case binary.ProgramTcm:
		return "tarantool-cluster-manager"
	case binary.ProgramUnknown, binary.ProgramCe, binary.ProgramTt:
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
