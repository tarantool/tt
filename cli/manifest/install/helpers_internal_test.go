package install

import (
	"archive/tar"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// LockDep is a test-local shorthand for a resolved lock dependency.
type LockDep = manifest.LockDependency

// archiveSpec describes a .tt archive to fabricate for a test, without a build
// or a real pack. It is the read side of what cli/manifest/pack writes.
type archiveSpec struct {
	// name is the package name written into the manifest.
	name string
	// version is the VERSION file content.
	version string
	// tarantoolConstraint / ttConstraint are the [platform] constraints; empty
	// values fall back to permissive defaults.
	tarantoolConstraint string
	ttConstraint        string
	// deps is the registry dependency map declared in [dependencies].
	deps map[string]string
	// lockDeps is the resolved dependency closure written into the lock.
	lockDeps []manifest.LockDependency
	// withRuntime bundles a fake _runtime/ and stamps bundled_* into the lock,
	// marking the archive as with-deps. Bundled tarantool version; "" means
	// --without-deps.
	withRuntime string
	// files are extra archive entries: archive-relative slash path -> content.
	// Used to place package and dependency subtrees under .rocks/.
	files map[string]string
}

// manifestTOML renders the spec's manifest.
func (s archiveSpec) manifestTOML() string {
	tnt := s.tarantoolConstraint
	if tnt == "" {
		tnt = ">=3.0.0,<4.0.0"
	}

	tt := s.ttConstraint
	if tt == "" {
		tt = ">=3.1.0"
	}

	out := "manifest_version = '0.1'\n\n[package]\nname = '" + s.name + "'\n\n" +
		"[platform]\ntarantool = '" + tnt + "'\ntt = '" + tt + "'\n\n" +
		"[products.default]\ncomponents = ['lua']\ndefault = true\n\n" +
		"[components.lua]\npath = '.'\n"

	if len(s.deps) == 0 {
		return out
	}

	names := make([]string, 0, len(s.deps))
	for n := range s.deps {
		names = append(names, n)
	}

	sort.Strings(names)

	var builder strings.Builder

	builder.WriteString(out)
	builder.WriteString("\n[dependencies]\n")

	for _, n := range names {
		builder.WriteString(n + " = '" + s.deps[n] + "'\n")
	}

	return builder.String()
}

// lockBytes builds the spec's lock, with manifest_hash matching the rendered
// manifest so a --locked install passes by default.
func (s archiveSpec) lockBytes(t *testing.T) []byte {
	t.Helper()

	lock := manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.0.0",
		ManifestHash:    manifest.HashBytes([]byte(s.manifestTOML())),
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: s.lockDeps},
		},
	}

	if s.withRuntime != "" {
		lock.BundledTarantool = s.withRuntime
		lock.BundledTt = "3.2.0"
	}

	data, err := lock.Marshal()
	require.NoError(t, err)

	return data
}

// build writes the archive to a temp file and returns its path.
func (s archiveSpec) build(t *testing.T) string {
	t.Helper()

	entries := map[string]string{
		manifestFileName: s.manifestTOML(),
		lockFileName:     string(s.lockBytes(t)),
		versionFileName:  s.version + "\n",
	}

	if s.withRuntime != "" {
		entries["_runtime/tarantool/bin/tarantool"] = "#!/bin/sh\n"
	}

	maps.Copy(entries, s.files)

	dst := filepath.Join(t.TempDir(), s.name+".tt")
	writeTarZst(t, dst, entries)

	return dst
}

// writeTarZst writes entries as a tar+zstd stream, mirroring the pack writer's
// normalized layout closely enough for the reader under test.
func writeTarZst(t *testing.T, dst string, entries map[string]string) {
	t.Helper()

	f, err := os.Create(dst) //nolint:gosec // Test writes to a temp path.
	require.NoError(t, err)

	defer func() { require.NoError(t, f.Close()) }()

	zstdWriter, err := zstd.NewWriter(f)
	require.NoError(t, err)

	tarWriter := tar.NewWriter(zstdWriter)

	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}

	sort.Strings(names)

	for _, name := range names {
		content := entries[name]

		mode := int64(0o644)
		if filepath.Base(name) == "tarantool" {
			mode = 0o755
		}

		require.NoError(t, tarWriter.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     mode,
			Size:     int64(len(content)),
			Format:   tar.FormatPAX,
		}))

		_, err := tarWriter.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, tarWriter.Close())
	require.NoError(t, zstdWriter.Close())
}

// rockFiles returns the archive entries for a rock's own subtree under .rocks/:
// a share module and its rock-manifest directory at the given version.
func rockFiles(name, version string) map[string]string {
	module := ".rocks/share/tarantool/" + name + "/init.lua"
	rockManifest := ".rocks/share/tarantool/rocks/" + name + "/" + version + "/rock"

	return map[string]string{
		module:       "-- " + name + "\n",
		rockManifest: name + " " + version + "\n",
	}
}

// mergeFiles unions several archive-entry maps.
func mergeFiles(fileMaps ...map[string]string) map[string]string {
	out := map[string]string{}

	for _, m := range fileMaps {
		maps.Copy(out, m)
	}

	return out
}

// readFile returns a file's content or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // Test reads from a temp path.
	require.NoError(t, err)

	return string(data)
}
