package search

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/tarantool/tt/sdk/v3/bundle"
	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/download"
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

// searchVersionsPrebuilt lists the versions of program, which has a Git
// repository, on a host without git: the releases the source of its
// prebuilt builds has for the host (binary.PrebuiltVersions), those tt
// binary install resolves a version among there. It warns that they are
// all it lists. No source, a source that fails and one with no build for
// the host are errors that say git is required, but a source whose TLS
// certificate cannot be verified (download.CertificateError): git would
// not verify it either, with the CA certificates of the same system.
func searchVersionsPrebuilt(ctx context.Context, program binary.Program) (
	version.VersionSlice, error,
) {
	lookup := "list the versions of " + program.String()

	source, err := binary.BundleSource(program)
	if err != nil {
		return nil, fmt.Errorf("%w to %s: install git", errGitRequired, lookup)
	}

	versions, err := binary.PrebuiltVersions(ctx, source, program)
	if certificate, ok := download.AsCertificateError(err); ok {
		return nil, fmt.Errorf("'tt binary search' cannot list the prebuilt %s in %s: %w",
			program, source.Name(), certificate)
	}

	if err != nil {
		return nil, fmt.Errorf("%w to %s: %w; install git", errGitRequired, lookup, err)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("%w to %s: %s has no prebuilt %s for %s/%s; install git",
			errGitRequired, lookup, source.Name(), program, runtime.GOOS, runtime.GOARCH)
	}

	log.Warnf("git is not found, so only the releases of %s with a prebuilt build in %s "+
		"for %s/%s are listed; install git to list every tag", program, source.Name(),
		runtime.GOOS, runtime.GOARCH)

	return versions, nil
}

// listBundles lists the builds the search asks for in source.
func listBundles(searchCtx *SearchCtx, source bundle.Source) (binary.Bundles, error) {
	return binary.ListBundles(context.Background(), source, searchCtx.Program,
		binary.NewBundleQuery(searchCtx.Program, searchCtx.DevBuilds, searchCtx.ReleaseVersion))
}
