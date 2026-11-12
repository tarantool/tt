package wasm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/rockspec"

	"github.com/tarantool/tt/v3/cli/manifest/rockindex"
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
	// moduleSO and moduleDylib are the names of the module in the rock: on
	// Linux, and on macOS, where Tarantool looks for C modules as ?.dylib.
	moduleSO    = "wasm.so"
	moduleDylib = "wasm.dylib"
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
   detailed = "The tarantool-wasm runtime: require('wasm'). Packed by tt.",
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
	// The name comes from a table a file may give: only the two names
	// Tarantool loads the module by are taken.
	var moduleName string

	switch platform.Module {
	case moduleSO:
		moduleName = moduleSO
	case moduleDylib:
		moduleName = moduleDylib
	default:
		return "", fmt.Errorf("%w: the module of %s is %q; it is %s or %s", ErrModuleName,
			platform.RockArch, platform.Module, moduleSO, moduleDylib)
	}

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
		//nolint:gosec // A fresh temporary directory and one of two constant names.
		err = os.WriteFile(filepath.Join(stage, rockLibDir, moduleName), module, modulePerm)
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
// and the tt-rocks-index.json beside them, so that a dependency declaring dir
// as its registry resolves and installs from it, and the lock pins the
// sha256 of the rock of every platform. Files that are neither rocks nor
// rockspecs are left out.
func IndexRegistry(ctx context.Context, dir string) error {
	return indexRegistry(ctx, dir, false)
}

// indexRegistry is IndexRegistry; ifIndexed writes the index only into a
// directory that has one already (rockindex.ServerOptions.IfIndexed).
func indexRegistry(ctx context.Context, dir string, ifIndexed bool) error {
	_, err := rockindex.MakeServer(ctx, dir, rockindex.ServerOptions{
		HTML: false, IfIndexed: ifIndexed, Warn: nil,
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
	// From is a local build of the module, packed as the rock of Arch instead
	// of the assets of the release, which are not downloaded. The rocks
	// already in Dir stay, so a run for each build fills a rock server.
	From string
	// Arch is the LuaRocks arch of From (linux-x86_64, macosx-aarch64);
	// empty is this host's.
	Arch string
}

// LocalPlatform is the platform of the rock of a local build for the LuaRocks
// arch: the module goes into the rock as wasm.so on Linux and as wasm.dylib
// on macOS, where Tarantool looks for C modules as ?.dylib only.
func LocalPlatform(arch string) (Platform, error) {
	if arch == "" {
		arch = hostRockArch()
	}

	var module string

	switch {
	case strings.HasPrefix(arch, "linux-"):
		module = moduleSO
	case strings.HasPrefix(arch, "macosx-"):
		module = moduleDylib
	default:
		return Platform{}, fmt.Errorf("%w: %s is not a linux-* or macosx-* arch",
			ErrUnsupportedPlatform, arch)
	}

	return Platform{
		GOOS: "", GOARCH: "", Asset: "", SHA256: "", RockArch: arch, Module: module,
	}, nil
}

// hostRockArch is the LuaRocks arch of the host tt runs on.
func hostRockArch() string {
	goos := map[string]string{"darwin": "macosx"}[runtime.GOOS]
	if goos == "" {
		goos = runtime.GOOS
	}

	goarch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	return goos + "-" + goarch
}

// packLocal packs the module opts.From names as the rock of opts.Arch.
func packLocal(opts PackOptions, release Release) (string, error) {
	platform, err := LocalPlatform(opts.Arch)
	if err != nil {
		return "", err
	}

	module, err := os.ReadFile(opts.From)
	if err != nil {
		return "", fmt.Errorf("reading the build: %w", err)
	}

	if opts.Progress != nil {
		_, _ = fmt.Fprintf(opts.Progress, "packing %s as the rock of %s\n",
			opts.From, platform.RockArch)
	}

	return BuildRock(opts.Dir, release, platform, module)
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

	if opts.From != "" {
		rock, err := packLocal(opts, release)
		if err != nil {
			return nil, err
		}

		err = IndexRegistry(ctx, opts.Dir)
		if err != nil {
			return nil, err
		}

		return []string{rock}, nil
	}

	if len(release.Platforms) == 0 {
		return nil, fmt.Errorf("%w: tarantool-wasm %s has no builds to download; "+
			"pack a local build of it (--from)", ErrUnsupportedPlatform, release.Version)
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
