package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/state"
)

// manifestTOML renders a minimal but valid manifest for a package, declaring the
// given registry dependency constraints.
func manifestTOML(name string, deps map[string]string) string {
	out := "manifest_version = '0.1'\n\n[package]\nname = '" + name + "'\n\n" +
		"[platform]\ntarantool = '>=3.0.0'\ntt = '>=3.1.0'\n\n" +
		"[products.default]\ncomponents = ['lua']\ndefault = true\n\n" +
		"[components.lua]\npath = '.'\n"

	if len(deps) == 0 {
		return out
	}

	var builder strings.Builder

	builder.WriteString(out)
	builder.WriteString("\n[dependencies]\n")

	for _, name := range sortedKeys(deps) {
		builder.WriteString(name + " = '" + deps[name] + "'\n")
	}

	return builder.String()
}

// lockTOML renders a lock pinning the given dependencies under the default
// product, with a manifest hash matching the rendered manifest.
func lockTOML(t *testing.T, manifestSource string, pins map[string]string) []byte {
	t.Helper()

	depList := make([]manifest.LockDependency, 0, len(pins))
	for _, name := range sortedKeys(pins) {
		depList = append(depList, manifest.LockDependency{
			Name: name, Version: pins[name], Source: "registry",
		})
	}

	lock := manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.0.0",
		ManifestHash:    manifest.HashBytes([]byte(manifestSource)),
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: depList},
		},
	}

	data, err := lock.Marshal()
	require.NoError(t, err)

	return data
}

// sortedKeys returns a map's keys in sorted order, so rendered TOML is stable.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	// A tiny insertion sort keeps the helper dependency-free and the order fixed.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	return keys
}

// writeGuest lays a guest package's metadata into manifests/<name>/, as an
// install would.
func writeGuest(t *testing.T, lay state.Layout, name, version string,
	deps, pins map[string]string,
) {
	t.Helper()

	source := manifestTOML(name, deps)

	require.NoError(t, state.WriteMetadata(
		lay, name, []byte(source), lockTOML(t, source, pins), version))
}

// writePrimary lays the project's own manifest, lock and VERSION into the
// install root, as a developed project would have them.
func writePrimary(t *testing.T, lay state.Layout, name, version string,
	deps, pins map[string]string,
) {
	t.Helper()

	source := manifestTOML(name, deps)

	writeFile(t, filepath.Join(lay.Root, state.PrimaryManifestFile), []byte(source))
	writeFile(t, filepath.Join(lay.Root, state.PrimaryLockFile), lockTOML(t, source, pins))

	if version != "" {
		writeFile(t, filepath.Join(lay.Root, state.PrimaryVersionFile), []byte(version+"\n"))
	}
}

// writeRock materializes a rock's files in the tree the way an install leaves
// them: a module under share/, a shared object under lib/, and a rock-manifest
// directory at the pinned version.
func writeRock(t *testing.T, lay state.Layout, name, version string) {
	t.Helper()

	writeFile(t, filepath.Join(lay.Share, name, "init.lua"), []byte("-- "+name+"\n"))
	writeFile(t, filepath.Join(lay.Lib, name, name+".so"), []byte("binary\n"))
	writeFile(t,
		filepath.Join(lay.Share, "rocks", name, version, "rock_manifest"),
		[]byte(name+" "+version+"\n"))
}

// writeFile writes content at path, creating parent directories.
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, content, 0o600))
}

// projectLayout resolves a project-scope layout over a fresh temp directory.
func projectLayout(t *testing.T) state.Layout {
	t.Helper()

	lay, err := state.ResolveLayout(state.ScopeProject, t.TempDir())
	require.NoError(t, err)

	return lay
}
