package wasm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/remote"
	"github.com/tarantool/go-luarocks/rockspec"
)

const (
	// registryManifestName is the index of a rock server: the upstream name,
	// which go-luarocks reads as well (manifest-5.1.zip, manifest-5.1,
	// manifest, manifest-5.1.json, in that order).
	registryManifestName = "manifest"
	// rockLibDir is the directory of a binary rock its native module is in.
	rockLibDir = "lib"
	// rockSuffix ends the file name of a rock.
	rockSuffix = ".rock"
	// dirPerm and filePerm are the modes the registry is written with.
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
	// modulePerm is the mode of the native module in the stage of the rock.
	modulePerm os.FileMode = 0o755
)

// RockFileName is the file name of the binary rock of platform.
func (r Release) RockFileName(platform Platform) string {
	return RockName + "-" + r.RockVersion() + "." + platform.RockArch + rockSuffix
}

// rockspec renders the rockspec the rock is packed with. It is the same for
// every platform: what differs is the module, which a binary rock carries
// built, so the build table says there is nothing left to build.
func (r Release) rockspec() string {
	return fmt.Sprintf(`rockspec_format = "3.0"
package = %q
version = %q
source = {
   url = %q,
   md5 = %q,
}
description = {
   summary = "Run WebAssembly components in Tarantool",
   detailed = "The tarantool-wasm runtime: require('wasm'). Packed by tt from the release assets.",
   homepage = "https://github.com/tarantool/tarantool-wasm-rs",
   license = "BSD-2-Clause",
}
supported_platforms = { "linux", "macosx" }
build = {
   type = "none",
}
`, RockName, r.RockVersion(), r.AssetURL(r.SDKAsset), r.SDKMD5)
}

// BuildRock packs module, the native build of platform, as the binary rock
// of the release in dir, and returns the rock's path. The rock is the one
// go-luarocks packs of the release's rock installed from a stage holding
// lib/<module> (client.PackStaged): the rockspec, lib/<module> and the
// rock_manifest upstream LuaRocks writes for it, with no modification times
// and no permissions, so the same module is the same rock. It is written to a
// temporary file and renamed into place, so the registry never serves half a
// rock.
func BuildRock(dir string, release Release, platform Platform, module []byte) (string, error) {
	specName := RockName + "-" + release.RockVersion() + ".rockspec"

	spec, err := rockspec.EvalSource(specName, []byte(release.rockspec()),
		luarocks.RockspecConfig{})
	if err != nil {
		return "", fmt.Errorf("the rockspec of the rock: %w", err)
	}

	stage, err := os.MkdirTemp("", "tt-wasm-rock-*")
	if err != nil {
		return "", fmt.Errorf("staging the rock: %w", err)
	}

	defer func() { _ = os.RemoveAll(stage) }()

	err = os.MkdirAll(filepath.Join(stage, rockLibDir), dirPerm)
	if err == nil {
		err = os.WriteFile(filepath.Join(stage, rockLibDir, platform.Module), module, modulePerm)
	}

	if err != nil {
		return "", fmt.Errorf("staging the rock: %w", err)
	}

	err = os.MkdirAll(dir, dirPerm)
	if err != nil {
		return "", fmt.Errorf("creating the registry: %w", err)
	}

	path := filepath.Join(dir, release.RockFileName(platform))

	err = client.PackStaged(spec, stage, path, client.PackStagedOpts{})
	if err != nil {
		return "", fmt.Errorf("packing the rock: %w", err)
	}

	return path, nil
}

// IndexRegistry writes the manifests of the rock server dir from the rocks
// it holds, as `luarocks-admin make_manifest` writes them (remote.MakeManifest),
// so that a dependency declaring dir as its registry resolves and installs
// from it. Files that are neither rocks nor rockspecs are left out.
func IndexRegistry(ctx context.Context, dir string) error {
	err := remote.MakeManifest(ctx, dir, remote.MakeManifestOpts{
		LuaVersions: nil, Index: false, Zip: false, Warn: nil,
	})
	if err != nil {
		return fmt.Errorf("indexing the registry: %w", err)
	}

	return nil
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory and a rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(filePerm)
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	err = os.Rename(tmp.Name(), path)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// PackOptions configure PackRocks.
type PackOptions struct {
	// Dir is the directory the rocks and their index go to: a rock server.
	Dir string
	// Version is the release; empty is DefaultVersion.
	Version string
	// Fetcher downloads the assets.
	Fetcher Fetcher
	// Progress receives one line per rock.
	Progress io.Writer
}

// PackRocks packs the rock of every platform of a release into a directory
// and indexes it, so that the directory, served over HTTP or uploaded next to
// the assets of the release, is the rock server tt wasm prepare --rock-server
// declares the dependency from: one for every machine, unlike the registry
// prepare packs for its own.
func PackRocks(ctx context.Context, opts PackOptions) ([]string, error) {
	version := opts.Version
	if version == "" {
		version = DefaultVersion
	}

	release, err := LookupRelease(version)
	if err != nil {
		return nil, err
	}

	rocks := make([]string, 0, len(release.Platforms))

	for _, platform := range release.Platforms {
		if opts.Progress != nil {
			_, _ = fmt.Fprintf(opts.Progress, "fetching %s\n", platform.Asset)
		}

		module, err := opts.Fetcher.Fetch(ctx, release, platform.Asset, platform.SHA256)
		if err != nil {
			return nil, err
		}

		rock, err := BuildRock(opts.Dir, release, platform, module)
		if err != nil {
			return nil, err
		}

		rocks = append(rocks, rock)
	}

	err = IndexRegistry(ctx, opts.Dir)
	if err != nil {
		return nil, err
	}

	return rocks, nil
}
