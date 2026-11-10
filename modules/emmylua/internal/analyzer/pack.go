package analyzer

import (
	"context"
	"fmt"
	"io"

	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/sdk/fetch"
)

// PackOptions configure PackRocks.
type PackOptions struct {
	// Dir is the directory the rocks and their index go to: a rock server.
	Dir string
	// Version is the release; empty is DefaultVersion.
	Version string
	// From, when set, is a local build - the directory of its programs -
	// packed as the rock of Arch instead of the downloaded builds.
	From string
	// Arch is the LuaRocks arch of the build in From; empty is the host's.
	Arch string
	// Fetcher downloads the assets.
	Fetcher fetch.Fetcher
	// Progress receives one line per rock.
	Progress io.Writer
}

// PackRocks packs rocks of a release into a directory and indexes it, so
// that the directory, served over HTTP or uploaded next to the assets of a
// release, is the rock server tt emmylua prepare --rock-server declares the
// dependency from: one for every machine, unlike the registry prepare packs
// for its own.
//
// Without From it packs the rock of every platform the release has builds
// to download for. With From it packs the one build, as the rock of Arch, and
// leaves the rocks already in Dir: run it once per platform to fill a server
// with builds made elsewhere.
func PackRocks(ctx context.Context, opts PackOptions) ([]string, error) {
	version := opts.Version
	if version == "" {
		version = DefaultVersion
	}

	release, err := LookupRelease(version)
	if err != nil {
		return nil, err
	}

	var rocks []string

	if opts.From != "" {
		rocks, err = packLocalRock(opts, release)
	} else {
		rocks, err = packReleaseRocks(ctx, opts, release)
	}

	if err != nil {
		return nil, err
	}

	err = indexRegistry(ctx, opts.Dir)
	if err != nil {
		return nil, err
	}

	return rocks, nil
}

// packLocalRock packs the build in opts.From as the rock of opts.Arch.
func packLocalRock(opts PackOptions, release Release) ([]string, error) {
	arch := opts.Arch
	if arch == "" {
		arch = remote.HostArch()
	}

	programs, err := ReadBuild(opts.From)
	if err != nil {
		return nil, err
	}

	rock, err := BuildRock(opts.Dir, release, arch, programs)
	if err != nil {
		return nil, err
	}

	return []string{rock}, nil
}

// packReleaseRocks packs the downloaded build of every platform of release.
func packReleaseRocks(ctx context.Context, opts PackOptions, release Release) ([]string, error) {
	if len(release.Platforms) == 0 {
		_, err := release.HostPlatform()

		return nil, err
	}

	rocks := make([]string, 0, len(release.Platforms))

	for _, platform := range release.Platforms {
		progressf(opts.Progress, "fetching EmmyLua %s for %s", release.Version, platform.Suffix)

		programs, err := fetchPrograms(ctx, opts.Fetcher, release, platform)
		if err != nil {
			return nil, err
		}

		rock, err := BuildRock(opts.Dir, release, platform.RockArch(), programs)
		if err != nil {
			return nil, err
		}

		rocks = append(rocks, rock)
	}

	return rocks, nil
}

// indexRegistry writes the manifests of the rock server dir from the rocks
// it holds, as `luarocks-admin make_manifest` writes them, so that a
// dependency declaring dir as its registry resolves and installs from it.
// Files that are neither rocks nor rockspecs are left out.
func indexRegistry(ctx context.Context, dir string) error {
	err := remote.MakeManifest(ctx, dir, remote.MakeManifestOpts{
		LuaVersions: nil, Index: false, Zip: false, Warn: nil,
	})
	if err != nil {
		return fmt.Errorf("indexing the registry: %w", err)
	}

	return nil
}
