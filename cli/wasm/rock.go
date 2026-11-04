package wasm

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // LuaRocks checksums rock files with md5.
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// registryManifestName is the index of a rock server: the upstream name,
	// which go-luarocks reads as well (manifest-5.1.zip, manifest-5.1,
	// manifest, manifest-5.1.json, in that order).
	registryManifestName = "manifest"
	// rockSuffix ends the file name of a rock.
	rockSuffix = ".rock"
	// dirPerm and filePerm are the modes the registry is written with.
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
	// modulePerm is the mode of the native module inside the rock.
	modulePerm os.FileMode = 0o755
)

// rockEpoch stamps the entries of a rock, so that one built twice from the
// same module is the same file: the zip format cannot go earlier than 1980.
//
//nolint:gochecknoglobals // A constant time: Go has no constant time.Time.
var rockEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// rockFileRe splits the file name of a binary rock into its name, its
// version-revision and its arch: wasm-0.1.0-1.linux-x86_64.rock.
var rockFileRe = regexp.MustCompile(`^(.+)-([^-]+-[0-9]+)\.([a-z0-9_-]+)\.rock$`)

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
// of the release in dir, and returns the rock's path. The rock holds the
// rockspec, lib/<module> and the rock_manifest upstream LuaRocks writes for a
// binary rock. It is written to a temporary file and renamed into place, so
// the registry never serves half a rock.
func BuildRock(dir string, release Release, platform Platform, module []byte) (string, error) {
	specName := RockName + "-" + release.RockVersion() + ".rockspec"
	spec := []byte(release.rockspec())
	modulePath := "lib/" + platform.Module

	manifest := fmt.Sprintf(`rock_manifest = {
   lib = {
      [%q] = %q,
   },
   [%q] = %q,
}
`, platform.Module, md5Hex(module), specName, md5Hex(spec))

	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	entries := []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{name: specName, mode: filePerm, data: spec},
		{name: "rock_manifest", mode: filePerm, data: []byte(manifest)},
		{name: modulePath, mode: modulePerm, data: module},
	}

	for _, entry := range entries {
		// Set field by field: a literal would have to name the deprecated
		// fields of the header too.
		var header zip.FileHeader

		header.Name = entry.name
		header.Method = zip.Deflate
		header.Modified = rockEpoch
		header.SetMode(entry.mode)

		file, err := writer.CreateHeader(&header)
		if err != nil {
			return "", fmt.Errorf("packing %s: %w", entry.name, err)
		}

		_, err = file.Write(entry.data)
		if err != nil {
			return "", fmt.Errorf("packing %s: %w", entry.name, err)
		}
	}

	err := writer.Close()
	if err != nil {
		return "", fmt.Errorf("packing the rock: %w", err)
	}

	err = os.MkdirAll(dir, dirPerm)
	if err != nil {
		return "", fmt.Errorf("creating the registry: %w", err)
	}

	path := filepath.Join(dir, release.RockFileName(platform))

	err = writeFileAtomic(path, archive.Bytes())
	if err != nil {
		return "", err
	}

	return path, nil
}

// IndexRegistry writes the manifest of the rock server dir from the binary
// rocks it holds, so that a dependency declaring dir as its registry resolves
// and installs from it. Files that are not binary rocks are left out.
func IndexRegistry(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading the registry: %w", err)
	}

	// The rocks by name, then by version: the arches each is built for.
	repository := map[string]map[string][]string{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		match := rockFileRe.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}

		name, version, arch := match[1], match[2], match[3]
		if repository[name] == nil {
			repository[name] = map[string][]string{}
		}

		repository[name][version] = append(repository[name][version], arch)
	}

	var out strings.Builder

	out.WriteString("commands = {}\nmodules = {}\nrepository = {\n")

	for _, name := range sortedKeys(repository) {
		fmt.Fprintf(&out, "   [%q] = {\n", name)

		for _, version := range sortedKeys(repository[name]) {
			fmt.Fprintf(&out, "      [%q] = {\n", version)

			arches := repository[name][version]
			slices.Sort(arches)

			for _, arch := range arches {
				fmt.Fprintf(&out, "         {\n            arch = %q,\n         },\n", arch)
			}

			out.WriteString("      },\n")
		}

		out.WriteString("   },\n")
	}

	out.WriteString("}\n")

	return writeFileAtomic(filepath.Join(dir, registryManifestName), []byte(out.String()))
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

// md5Hex returns the md5 of data in hex, as rock_manifest records it.
func md5Hex(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // LuaRocks checksums rock files with md5.

	return hex.EncodeToString(sum[:])
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

	err = IndexRegistry(opts.Dir)
	if err != nil {
		return nil, err
	}

	return rocks, nil
}
