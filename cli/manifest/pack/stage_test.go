package pack

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// stagedNames lists every file staged under dir, slash-separated and sorted.
func stagedNames(t *testing.T, dir string) []string {
	t.Helper()

	var names []string

	require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		names = append(names, filepath.ToSlash(rel))

		return nil
	}))

	sort.Strings(names)

	return names
}

// testProject writes a minimal project tree and returns its dir and a parsed
// manifest for it.
func testProject(t *testing.T, extra map[string]string) (string, *manifest.Manifest) {
	t.Helper()

	dir := t.TempDir()

	manifestTOML := `manifest_version = "0.1"

[package]
name = "my-app"
include = ["README.md"]
license_files = ["LICENSE"]

[platform]
tarantool = ">=3.0.0,<4.0.0"
tt = ">=2.0.0,<3.0.0"

[components.main]
path = "."

[products.default]
components = ["main"]
default = true
`

	files := map[string]string{
		manifestFileName: manifestTOML,
		"README.md":      "readme body",
		"LICENSE":        "license body",
	}
	for k, v := range extra {
		files[k] = v
	}

	writeTree(t, dir, files)

	man, _, err := manifest.ParseManifest([]byte(manifestTOML))
	require.NoError(t, err)

	return dir, man
}

// testTree writes a .rocks tree with the package's own files plus a dependency.
func testTree(t *testing.T, projectDir string) string {
	t.Helper()

	tree := filepath.Join(projectDir, ".rocks")
	writeTree(t, tree, map[string]string{
		"share/tarantool/my-app/init.lua":    "return 1",
		"share/tarantool/my-app/version.lua": "return {}",
		"share/tarantool/inspect/init.lua":   "dependency",
		"lib/tarantool/my-app/fast.so":       "native",
		"lib/tarantool/cjson/cjson.so":       "dependency native",
	})

	return tree
}

func baseRequest(projectDir string, man *manifest.Manifest, tree string) stageRequest {
	return stageRequest{
		ProjectDir: projectDir,
		Manifest:   man,
		LockBytes:  []byte("lock_version = \"0.1\"\n"),
		Version:    "1.0.0",
		Tree:       tree,
		WithDeps:   true,
		Namespaces: []string{"my-app"},
	}
}

func TestStageWithDeps(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)
	stageDir := t.TempDir()

	require.NoError(t, stage(stageDir, baseRequest(projectDir, man, tree)))

	assert.Equal(t, []string{
		".rocks/lib/tarantool/cjson/cjson.so",
		".rocks/lib/tarantool/my-app/fast.so",
		".rocks/share/tarantool/inspect/init.lua",
		".rocks/share/tarantool/my-app/init.lua",
		".rocks/share/tarantool/my-app/version.lua",
		"LICENSE",
		"README.md",
		"VERSION",
		"app.manifest.lock",
		"app.manifest.toml",
	}, stagedNames(t, stageDir))

	version, err := os.ReadFile(filepath.Join(stageDir, versionFileName))
	require.NoError(t, err)
	assert.Equal(t, "1.0.0\n", string(version))

	// The manifest goes in verbatim, not re-serialized.
	staged, err := os.ReadFile(filepath.Join(stageDir, manifestFileName))
	require.NoError(t, err)
	assert.Equal(t, string(man.Raw()), string(staged))
}

// TestStageWithoutDeps is the core of --without-deps: the package's own
// namespace survives in .rocks/, every foreign dependency is dropped.
func TestStageWithoutDeps(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)
	stageDir := t.TempDir()

	req := baseRequest(projectDir, man, tree)
	req.WithDeps = false

	require.NoError(t, stage(stageDir, req))

	names := stagedNames(t, stageDir)

	assert.Contains(t, names, ".rocks/share/tarantool/my-app/init.lua")
	assert.Contains(t, names, ".rocks/lib/tarantool/my-app/fast.so")
	assert.NotContains(t, names, ".rocks/share/tarantool/inspect/init.lua")
	assert.NotContains(t, names, ".rocks/lib/tarantool/cjson/cjson.so")

	// Metadata and license are unaffected by the mode.
	assert.Contains(t, names, "app.manifest.toml")
	assert.Contains(t, names, "app.manifest.lock")
	assert.Contains(t, names, "VERSION")
	assert.Contains(t, names, "LICENSE")
}

func TestStageMissingTreeIsNotFatal(t *testing.T) {
	projectDir, man := testProject(t, nil)
	stageDir := t.TempDir()

	req := baseRequest(projectDir, man, filepath.Join(projectDir, ".rocks"))

	require.NoError(t, stage(stageDir, req))
	assert.Contains(t, stagedNames(t, stageDir), "app.manifest.toml")
}

// TestStageEntryRejections covers the three ways a manifest-declared payload
// entry is refused. A silently dropped LICENSE is worse than a failed pack.
func TestStageEntryRejections(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		want  error
	}{
		{"missing file", "NOPE.md", errMissingInclude},
		{"reserved name", "VERSION", errReservedName},
		{"reserved directory", ".rocks", errReservedName},
		{"escaping path", "../outside.txt", errEscapingPath},
		{"absolute path", "/etc/passwd", errEscapingPath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			err := stageEntry(t.TempDir(), projectDir, tt.entry, "include")
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.want)
			assert.Equal(t, exitStateError, ExitCode(err))
		})
	}
}

// TestCopyTreeFollowsSymlinkedRoot is a regression test. WalkDir does not
// follow a symlinked root, so it reported the link as a plain entry and the
// directory went down the file-copy path, failing with "is a directory".
// Package prefixes hit this routinely - Homebrew's share/tarantool is a link
// into the Cellar, which broke bundling a fallback Tarantool's share/ tree.
func TestCopyTreeFollowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()

	real := filepath.Join(base, "real")
	writeTree(t, real, map[string]string{"a.lua": "a", "sub/b.lua": "b"})

	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(real, link))

	dst := t.TempDir()
	require.NoError(t, copyTree(link, dst))

	assert.Equal(t, []string{"a.lua", "sub/b.lua"}, stagedNames(t, dst))
}

// TestCopyTreeDereferencesInnerSymlinks covers links found during the walk: the
// archive carries no link structure, so each is copied as its target.
func TestCopyTreeDereferencesInnerSymlinks(t *testing.T) {
	base := t.TempDir()

	src := filepath.Join(base, "src")
	writeTree(t, src, map[string]string{"real.lua": "body", "d/inner.lua": "inner"})

	require.NoError(t, os.Symlink(
		filepath.Join(src, "real.lua"), filepath.Join(src, "link.lua")))
	require.NoError(t, os.Symlink(
		filepath.Join(src, "d"), filepath.Join(src, "linkdir")))
	// A dangling link must be skipped, not fail the pack.
	require.NoError(t, os.Symlink(
		filepath.Join(src, "gone.lua"), filepath.Join(src, "dangling.lua")))

	dst := t.TempDir()
	require.NoError(t, copyTree(src, dst))

	assert.Equal(t, []string{
		"d/inner.lua", "link.lua", "linkdir/inner.lua", "real.lua",
	}, stagedNames(t, dst))

	body, err := os.ReadFile(filepath.Join(dst, "link.lua"))
	require.NoError(t, err)
	assert.Equal(t, "body", string(body))
}

// TestStageEntrySelfCopyIsRejected is a regression test. The staging directory
// lives inside the project at _build/pack/stage-*, so include = ["."] had
// copyTree descend into the copies it was creating, dying at PATH_MAX with an
// opaque "file name too long" for an entirely plausible manifest.
func TestStageEntrySelfCopyIsRejected(t *testing.T) {
	for _, entry := range []string{".", "_build", "_build/pack"} {
		t.Run(entry, func(t *testing.T) {
			projectDir := t.TempDir()
			writeTree(t, projectDir, map[string]string{"init.lua": "x"})

			stageDir := filepath.Join(projectDir, "_build", "pack", "stage-1")

			err := stageEntry(stageDir, projectDir, entry, "include")
			require.Error(t, err)
			assert.ErrorIs(t, err, errReservedName)
		})
	}
}

// TestCopyTreeRefusesSelfNesting guards the invariant directly, independent of
// which entries isReservedName happens to reject.
func TestCopyTreeRefusesSelfNesting(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"a.lua": "a"})

	err := copyTree(src, filepath.Join(src, "_build", "pack", "stage-1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to copy")
}

// TestStageReservedNameIsCaseInsensitive is a regression test. On a
// case-insensitive filesystem (APFS, the dev platform) include = ["version"]
// reopened the already-staged VERSION with O_TRUNC and replaced tt's own
// metadata with the package's file — silent archive corruption.
func TestStageReservedNameIsCaseInsensitive(t *testing.T) {
	projectDir, man := testProject(t, map[string]string{"version": "NOT THE VERSION"})
	man.Package.Include = []string{"version"}

	err := stage(t.TempDir(), baseRequest(projectDir, man, filepath.Join(projectDir, ".rocks")))

	require.Error(t, err)
	assert.ErrorIs(t, err, errReservedName)
}

// TestStageWithoutDepsRejectsFlatNamespace is a regression test. A component
// with namespace = "" lays its files at the rocks-tree root, where they cannot
// be told apart from a dependency's; the filter used to skip it silently,
// dropping the package's own code from an archive that promises to keep it.
func TestStageWithoutDepsRejectsFlatNamespace(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)

	req := baseRequest(projectDir, man, tree)
	req.WithDeps = false
	req.HasFlatNamespace = true

	err := stage(t.TempDir(), req)

	require.Error(t, err)
	assert.ErrorIs(t, err, errFlatNamespace)
	assert.Equal(t, exitStateError, ExitCode(err))
}

// A flat namespace is fine with deps bundled: the whole tree is copied anyway.
func TestStageWithDepsAllowsFlatNamespace(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)

	req := baseRequest(projectDir, man, tree)
	req.HasFlatNamespace = true

	require.NoError(t, stage(t.TempDir(), req))
}

func TestStageEntryCopiesDirectory(t *testing.T) {
	projectDir := t.TempDir()
	writeTree(t, projectDir, map[string]string{
		"doc/a.md":     "a",
		"doc/sub/b.md": "b",
	})

	stageDir := t.TempDir()
	require.NoError(t, stageEntry(stageDir, projectDir, "doc", "include"))

	assert.Equal(t, []string{"doc/a.md", "doc/sub/b.md"}, stagedNames(t, stageDir))
}
