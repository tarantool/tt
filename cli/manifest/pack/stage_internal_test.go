package pack

import (
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	lrtree "github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
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
	maps.Copy(files, extra)

	writeTree(t, dir, files)

	man, _, err := manifest.ParseManifest([]byte(manifestTOML))
	require.NoError(t, err)

	return dir, man
}

// testTree installs into the project's .rocks tree the package's rock -
// its components, version.lua and the programs of its executable components -
// and two dependencies, one with a console script.
func testTree(t *testing.T, projectDir string) string {
	t.Helper()

	tree := filepath.Join(projectDir, ".rocks")
	treetest.Install(t, tree,
		treetest.Rock{Name: "my-app", Version: "1.0.0", Files: map[string]string{
			"lua/my-app/init.lua":    "return 1",
			"lua/my-app/version.lua": "return {}",
			"lib/my-app/fast.so":     "native",
			"bin/gw":                 "own program",
		}},
		treetest.Rock{Name: "inspect", Version: "3.1.3", Files: map[string]string{
			"lua/inspect/init.lua": "dependency",
		}},
		treetest.Rock{Name: "cjson", Version: "2.1.0", Files: map[string]string{
			"lib/cjson/cjson.so": "dependency native",
			"bin/json2lua":       "#!/usr/bin/env tarantool\nprint(1)\n",
		}})

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
		DevOnly:    nil,
		Warn:       nil,
	}
}

// deployed lists the staged files of .rocks/ outside its rocks dir: what the
// rocks deployed.
func deployed(names []string) []string {
	var out []string

	for _, name := range names {
		rest, ok := strings.CutPrefix(name, ".rocks/")
		if ok && !strings.HasPrefix(rest, rocksInstallDir+"/") {
			out = append(out, name)
		}
	}

	return out
}

// checkArchiveTree checks that the staged .rocks of stageDir is a tree whose
// manifest lists every rock it holds and every file they deployed.
func checkArchiveTree(t *testing.T, stageDir string) {
	t.Helper()

	archiveTree, err := lrtree.Open(luarocks.Config{Tree: filepath.Join(stageDir, rocksDirName)})
	require.NoError(t, err)

	report, err := archiveTree.Check(t.Context(), lrtree.CheckOpts{})
	require.NoError(t, err)
	assert.True(t, report.OK(), "%v", report.Issues)
}

func TestStageWithDeps(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)
	stageDir := t.TempDir()

	require.NoError(t, stage(t.Context(), stageDir, baseRequest(projectDir, man, tree)))

	names := stagedNames(t, stageDir)

	assert.Equal(t, []string{
		".rocks/bin/gw",
		".rocks/bin/json2lua",
		".rocks/lib/tarantool/cjson/cjson.so",
		".rocks/lib/tarantool/my-app/fast.so",
		".rocks/share/tarantool/inspect/init.lua",
		".rocks/share/tarantool/my-app/init.lua",
		".rocks/share/tarantool/my-app/version.lua",
	}, deployed(names))

	for _, name := range []string{
		"LICENSE", "README.md", "VERSION", "app.manifest.lock", "app.manifest.toml",
		".rocks/share/tarantool/rocks/manifest",
		".rocks/share/tarantool/rocks/my-app/1.0.0-1/rock_manifest",
		".rocks/share/tarantool/rocks/cjson/2.1.0-1/rock_manifest",
	} {
		assert.Contains(t, names, name)
	}

	assert.NotContains(t, names, ".rocks/share/tarantool/rocks/.lock")
	checkArchiveTree(t, stageDir)

	// A command is its script in the archive, not a wrapper naming the paths
	// of this machine: the install writes the one its tree needs.
	script, err := os.ReadFile(filepath.Join(stageDir, ".rocks", "bin", "json2lua"))
	require.NoError(t, err)
	assert.Equal(t, "#!/usr/bin/env tarantool\nprint(1)\n", string(script))

	version, err := os.ReadFile(filepath.Join(stageDir, versionFileName))
	require.NoError(t, err)
	assert.Equal(t, "1.0.0\n", string(version))

	// The manifest goes in verbatim, not re-serialized.
	staged, err := os.ReadFile(filepath.Join(stageDir, manifestFileName))
	require.NoError(t, err)
	assert.Equal(t, string(man.Raw()), string(staged))
}

// TestStageWithoutDeps is the core of --without-deps: the package's own rock
// survives in .rocks/, its programs included, every foreign dependency is
// dropped.
func TestStageWithoutDeps(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)
	stageDir := t.TempDir()

	req := baseRequest(projectDir, man, tree)

	req.WithDeps = false

	require.NoError(t, stage(t.Context(), stageDir, req))

	names := stagedNames(t, stageDir)

	assert.Equal(t, []string{
		".rocks/bin/gw",
		".rocks/lib/tarantool/my-app/fast.so",
		".rocks/share/tarantool/my-app/init.lua",
		".rocks/share/tarantool/my-app/version.lua",
	}, deployed(names))
	assert.NotContains(t, names, ".rocks/share/tarantool/rocks/inspect/3.1.3-1/rock_manifest")
	checkArchiveTree(t, stageDir)

	// Metadata and license are unaffected by the mode.
	assert.Contains(t, names, "app.manifest.toml")
	assert.Contains(t, names, "app.manifest.lock")
	assert.Contains(t, names, "VERSION")
	assert.Contains(t, names, "LICENSE")
}

// TestStageLeavesOutWhatNoRockOwns: a file of the project's tree no rock
// lists - laid there by hand, or by an older tt - does not reach the archive,
// and the pack says so.
func TestStageLeavesOutWhatNoRockOwns(t *testing.T) {
	projectDir, man := testProject(t, nil)
	tree := testTree(t, projectDir)
	writeTree(t, tree, map[string]string{
		"bin/gw-cli":                       "program of another product",
		"share/tarantool/my-app/stale.lua": "left by an older build",
	})

	var warned []string

	req := baseRequest(projectDir, man, tree)

	req.Warn = func(msg string) { warned = append(warned, msg) }

	stageDir := t.TempDir()
	require.NoError(t, stage(t.Context(), stageDir, req))

	names := stagedNames(t, stageDir)
	assert.NotContains(t, names, ".rocks/bin/gw-cli")
	assert.NotContains(t, names, ".rocks/share/tarantool/my-app/stale.lua")

	require.Len(t, warned, 1)
	assert.Contains(t, warned[0], "2 files")
	assert.Contains(t, warned[0], "bin/gw-cli")
}

// TestStageWithoutDepsKeepsAFlatNamespace: a component with namespace = ""
// lays its files at the top of the rocks tree, which the package's rock owns
// as it owns any other, so --without-deps packs them.
func TestStageWithoutDepsKeepsAFlatNamespace(t *testing.T) {
	projectDir, man := testProject(t, nil)

	tree := filepath.Join(projectDir, ".rocks")
	treetest.Install(t, tree,
		treetest.Rock{Name: "my-app", Version: "1.0.0", Files: map[string]string{
			"lua/flat.lua": "return 1",
		}},
		treetest.Rock{Name: "inspect", Version: "3.1.3", Files: map[string]string{
			"lua/inspect.lua": "dependency",
		}})

	req := baseRequest(projectDir, man, tree)

	req.WithDeps = false

	stageDir := t.TempDir()
	require.NoError(t, stage(t.Context(), stageDir, req))

	assert.Equal(t, []string{".rocks/share/tarantool/flat.lua"},
		deployed(stagedNames(t, stageDir)))
}

func TestStageMissingTreeIsNotFatal(t *testing.T) {
	projectDir, man := testProject(t, nil)
	stageDir := t.TempDir()

	req := baseRequest(projectDir, man, filepath.Join(projectDir, ".rocks"))

	require.NoError(t, stage(t.Context(), stageDir, req))
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
			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
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

	target := filepath.Join(base, "real")
	writeTree(t, target, map[string]string{"a.lua": "a", "sub/b.lua": "b"})

	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(target, link))

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

	err := stage(t.Context(), t.TempDir(),
		baseRequest(projectDir, man, filepath.Join(projectDir, ".rocks")))

	require.Error(t, err)
	assert.ErrorIs(t, err, errReservedName)
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
