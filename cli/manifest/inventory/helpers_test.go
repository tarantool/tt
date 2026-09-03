package inventory_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/state"
)

// tree is a fixture install tree: a project directory with a .rocks/ layout that
// tests populate the way an install would, without ever running one.
type tree struct {
	// dir is the install-root (the project directory for project scope).
	dir string
	// lay is the resolved layout of the scope under test.
	lay state.Layout
}

// newTree resolves a fresh install tree for the given scope over a temp
// directory. Only the project scope is rooted in a temp dir; user and system
// point at real machine paths, so tests that need those layouts build the
// project layout and pass the scope separately.
func newTree(t *testing.T) tree {
	t.Helper()

	dir := t.TempDir()

	lay, err := state.ResolveLayout(state.ScopeProject, dir)
	require.NoError(t, err)

	return tree{dir: dir, lay: lay}
}

// pkg describes one package to lay into a fixture tree.
type pkg struct {
	// name is the package name.
	name string
	// version is the recorded version, also the version its files sit at.
	version string
	// description is the manifest's package description.
	description string
	// deps are the version constraints its manifest declares.
	deps map[string]string
	// pins are the exact versions its lock records, keyed by rock name. Every
	// pinned rock gets files in the tree too.
	pins map[string]string
	// noLock omits the lock entirely, as a guest installed by a tt that predates
	// lock metadata would be.
	noLock bool
}

// installGuest lays a guest package into the tree: its metadata under
// manifests/<name>/, its own files, and the files of every rock it pins.
func (tr tree) installGuest(t *testing.T, spec pkg) {
	t.Helper()

	source := manifestTOML(spec)

	var lockBytes []byte

	if !spec.noLock {
		lockBytes = lockTOML(t, source, spec.pins)
	}

	require.NoError(t,
		state.WriteMetadata(tr.lay, spec.name, []byte(source), lockBytes, spec.version))

	tr.installFiles(t, spec)
}

// installGuestManifest lays a guest whose manifest is supplied verbatim, for
// shapes the pkg spec does not render (a component-level dependency, say).
func (tr tree) installGuestManifest(
	t *testing.T, name, version, manifestSource string, pins map[string]string,
) {
	t.Helper()

	require.NoError(t, state.WriteMetadata(
		tr.lay, name, []byte(manifestSource), lockTOML(t, manifestSource, pins), version))

	tr.installRock(t, name, version)

	for rock, rockVersion := range pins {
		tr.installRock(t, rock, rockVersion)
	}
}

// manifestWithComponentDep renders a manifest declaring its dependency on the
// component rather than in the global map. The resolver treats both as
// declarations, so the inventory has to as well.
func manifestWithComponentDep(name, dep, constraint string) string {
	return "manifest_version = '0.1'\n\n[package]\nname = '" + name + "'\n\n" +
		"[platform]\ntarantool = '>=3.0.0'\ntt = '>=3.1.0'\n\n" +
		"[products.default]\ncomponents = ['lua']\ndefault = true\n\n" +
		"[components.lua]\npath = '.'\n\n" +
		"[components.lua.dependencies]\n" + dep + " = '" + constraint + "'\n"
}

// installPrimary lays the project's own package into the install root: its
// manifest, lock and VERSION, plus the files of every rock it pins.
func (tr tree) installPrimary(t *testing.T, spec pkg) {
	t.Helper()

	source := manifestTOML(spec)

	tr.write(t, filepath.Join(tr.lay.Root, state.PrimaryManifestFile), source)

	if !spec.noLock {
		tr.write(t, filepath.Join(tr.lay.Root, state.PrimaryLockFile),
			string(lockTOML(t, source, spec.pins)))
	}

	if spec.version != "" {
		tr.write(t, filepath.Join(tr.lay.Root, state.PrimaryVersionFile), spec.version+"\n")
	}

	tr.installFiles(t, spec)
}

// installFiles materializes the package's own rock subtree and one for every
// rock it pins. Rocks pinned by several packages are simply written twice, which
// is what sharing one copy in the tree looks like.
func (tr tree) installFiles(t *testing.T, spec pkg) {
	t.Helper()

	if spec.version != "" {
		tr.installRock(t, spec.name, spec.version)
	}

	for name, version := range spec.pins {
		tr.installRock(t, name, version)
	}
}

// installRock writes the three places one rock version occupies: a module under
// share/, a shared object under lib/, and a rock-manifest directory.
func (tr tree) installRock(t *testing.T, name, version string) {
	t.Helper()

	tr.write(t, filepath.Join(tr.lay.Share, name, "init.lua"), "-- "+name+"\n")
	tr.write(t, filepath.Join(tr.lay.Lib, name, name+".so"), "binary\n")
	tr.write(t,
		filepath.Join(tr.lay.Share, "rocks", name, version, "rock_manifest"),
		name+" "+version+"\n")
}

// writeTreeManifest lays the LuaRocks tree manifest that records which rock
// requires which, at <share>/rocks/manifest.
//
// It is written by hand in LuaRocks' own Lua-table format rather than through a
// serializer, so the reader under test is exercised against the real on-disk
// shape and not against a round trip of its own assumptions.
func (tr tree) writeTreeManifest(
	t *testing.T, edges map[string][]string, versions map[string]string,
) {
	t.Helper()

	var builder strings.Builder

	// LuaRocks manifests are bare assignments loaded into an environment, not a
	// returned table — the parser rejects a "return {...}" wrapper.
	builder.WriteString("commands = {}\nmodules = {}\nrepository = {}\ndependencies = {\n")

	for _, rock := range sortedKeys(edges) {
		version := versions[rock]
		if version == "" {
			version = "0.0.0-1"
		}

		// Bracket-quote every key: a rock name with a hyphen is not a valid Lua
		// bare key, and LuaRocks quotes such names too.
		builder.WriteString("   [\"" + rock + "\"] = {\n")
		builder.WriteString("      [\"" + version + "\"] = {\n")

		for _, dep := range edges[rock] {
			builder.WriteString("         {\n")
			builder.WriteString("            name = \"" + dep + "\",\n")
			builder.WriteString("            constraints = {}\n")
			builder.WriteString("         },\n")
		}

		builder.WriteString("      }\n   },\n")
	}

	builder.WriteString("}\n")

	tr.write(t, filepath.Join(tr.lay.Share, "rocks", "manifest"), builder.String())
}

// write creates a file and its parents.
func (tr tree) write(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// rockExists reports whether a rock still has files anywhere in the tree. It
// checks all three locations, so a partial removal fails the assertion rather
// than passing on the one directory that happened to go.
func (tr tree) rockExists(name string) bool {
	for _, path := range []string{
		filepath.Join(tr.lay.Share, name),
		filepath.Join(tr.lay.Lib, name),
		filepath.Join(tr.lay.Share, "rocks", name),
	} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	return false
}

// rockManifestPath is where one version of a rock records itself in the tree.
func (tr tree) rockManifestPath(name, version string) string {
	return filepath.Join(tr.lay.Share, "rocks", name, version)
}

// removeAll deletes a path, for fixtures that simulate a tree diverging from
// what a lock recorded.
func removeAll(path string) error {
	return os.RemoveAll(path)
}

// metadataExists reports whether a package still has metadata under manifests/.
func (tr tree) metadataExists(name string) bool {
	_, err := os.Stat(filepath.Join(tr.lay.Manifests, name))

	return err == nil
}

// manifestTOML renders a package's manifest from its spec.
func manifestTOML(spec pkg) string {
	var builder strings.Builder

	builder.WriteString("manifest_version = '0.1'\n\n[package]\nname = '" + spec.name + "'\n")

	if spec.description != "" {
		builder.WriteString("description = '" + spec.description + "'\n")
	}

	builder.WriteString("\n[platform]\ntarantool = '>=3.0.0'\ntt = '>=3.1.0'\n\n" +
		"[products.default]\ncomponents = ['lua']\ndefault = true\n\n" +
		"[components.lua]\npath = '.'\n")

	if len(spec.deps) > 0 {
		builder.WriteString("\n[dependencies]\n")

		for _, name := range sortedKeys(spec.deps) {
			builder.WriteString(name + " = '" + spec.deps[name] + "'\n")
		}
	}

	return builder.String()
}

// lockTOML renders a lock pinning the given rocks under the default product,
// with a manifest hash matching the rendered manifest.
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

// sortedKeys returns a map's keys in sorted order, so rendered fixtures are
// stable.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
