package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var errNoPackagesFoundForThisOSOrReleaseVersion = errors.New(
	"no packages found for this OS or release version",
)

// searchVersionsBundles lists the builds of the product in the source of
// this build of tt that has its archives.
func searchVersionsBundles(searchCtx *SearchCtx) (version.VersionSlice, error) {
	source, err := binary.BundleSource(searchCtx.Program)
	if err != nil {
		return nil, err
	}

	bundles, err := listBundles(searchCtx, source)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bundle info for %s: %w", searchCtx.Program, err)
	}

	var versions version.VersionSlice

	for _, found := range bundles {
		switch searchCtx.Filter {
		case SearchRelease:
			if found.IsDebug() {
				continue
			}
		case SearchDebug:
			if !found.IsDebug() {
				continue
			}
		case SearchAll:
			// Keep both release and debug builds.
		}

		versions = append(versions, found.Version)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("failed to fetch bundle info for %s: %w",
			searchCtx.Program, errNoPackagesFoundForThisOSOrReleaseVersion)
	}

	return versions, nil
}

// listBundles lists the builds the search asks for in source.
func listBundles(searchCtx *SearchCtx, source bundle.Source) (binary.Bundles, error) {
	return binary.ListBundles(context.Background(), source, searchCtx.Program,
		binary.NewBundleQuery(searchCtx.Program, searchCtx.DevBuilds, searchCtx.ReleaseVersion))
}
