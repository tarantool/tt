package distbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unpushedSDK is a pseudo-version of the SDK at a commit no module proxy
// has: what a module requires while it is developed with a change of the
// core not pushed yet.
const unpushedSDK = "v3.0.0-20991231235959-0123456789ab"

// TestRenderWorkspace checks the go.work a workspace spec makes: the mark,
// the go directive, the modules it uses and the version-less replacements,
// by paths relative to the file.
func TestRenderWorkspace(t *testing.T) {
	t.Parallel()

	got := renderWorkspace(workSpec{
		Dir:      "/src/dist",
		Manifest: "/src/dist/tt.manifest.toml",
		Core:     "/src/tt",
		Go:       "1.27.4",
		Use:      []string{"/src/dist", "/src/dist/plugins/hello", "/src/tt-foo"},
		Replace: []workReplace{
			{Module: "example.com/lib", Version: "v1.2.3", Dir: "/src/dist/libs/lib"},
			{Module: "github.com/tarantool/tt/sdk/v3", Version: "", Dir: "/src/tt/sdk"},
			{Module: "github.com/tarantool/tt/v3", Version: "", Dir: "/src/tt"},
		},
	})

	assert.Equal(t, `// Written by tt binary workspace: the local modules of the distribution
// ./tt.manifest.toml on the SDK of the core ../tt,
// whatever their go.mod requires. tt binary workspace --remove deletes it.
// Keep it out of git.

go 1.27.4

use (
	.
	./plugins/hello
	../tt-foo
)

replace (
	example.com/lib v1.2.3 => ./libs/lib
	github.com/tarantool/tt/sdk/v3 => ../tt/sdk
	github.com/tarantool/tt/v3 => ../tt
)
`, string(got))
	assert.True(t, strings.HasPrefix(string(got), workspaceMark))
}

// TestWorkPath checks how a go.work names a directory.
func TestWorkPath(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/src/dist":             ".",
		"/src/dist/plugins/foo": "./plugins/foo",
		"/src":                  "..",
		"/src/tt/sdk":           "../tt/sdk",
		"/src/my module":        `"../my module"`,
	}

	for path, want := range cases {
		assert.Equal(t, want, workPath("/src/dist", path), path)
	}
}

// TestWorkPathUnderALink: a go.work in a directory under a symbolic link, as
// the temporary directories of macOS are under /var, a link to
// /private/var, names a directory out of the link by its absolute path. A
// relative one would lead elsewhere from the directory with the link
// resolved, which the go command may start from.
func TestWorkPathUnderALink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	private := filepath.Join(root, "private", "var")
	core := filepath.Join(root, "core", "sdk")

	require.NoError(t, os.MkdirAll(filepath.Join(private, "dist", "plugins"), 0o755))
	require.NoError(t, os.MkdirAll(core, 0o755))
	require.NoError(t, os.Symlink(private, filepath.Join(root, "var")))

	dist := filepath.Join(root, "var", "dist")

	assert.Equal(t, core, workPath(dist, core))
	assert.Equal(t, "./plugins", workPath(dist, filepath.Join(dist, "plugins")))
}

// TestNewerGo checks that the go directive is the newest of the go.mod
// files, an empty or invalid one older than any.
func TestNewerGo(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.27.4", newerGo("1.27.0", "1.27.4"))
	assert.Equal(t, "1.27.4", newerGo("1.27.4", "1.26"))
	assert.Equal(t, "1.27rc1", newerGo("1.26.10", "1.27rc1"))
	assert.Equal(t, "1.21", newerGo("", "1.21"))
	assert.Equal(t, "1.21", newerGo("1.21", ""))
	assert.Equal(t, "1.21", newerGo("1.21", "x"))
}

// needGo skips a test that runs the go command when there is none.
func needGo(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}
}

// fakeCore writes a core into dir: the core module, which replaces an SDK
// module and a standard module with its directories, and an SDK module its
// go.mod does not name, which the workspace finds under sdk/ all the same.
// The SDK exports Where, which says that it is the local one.
func fakeCore(t *testing.T, dir string) {
	t.Helper()

	writeFile(t, filepath.Join(dir, "go.mod"), `module github.com/tarantool/tt/v3

go 1.21

require github.com/tarantool/tt/sdk/v3 v3.0.0-0

replace (
	github.com/tarantool/tt/modules/aeon => ./modules/aeon
	github.com/tarantool/tt/sdk/v3 => ./sdk
)
`)
	writeFile(t, filepath.Join(dir, "core.go"), "package core\n")
	writeFile(t, filepath.Join(dir, "sdk", "go.mod"),
		"module github.com/tarantool/tt/sdk/v3\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sdk", "sdk.go"), "package sdk\n\n"+
		"// Where tells which SDK this is.\nfunc Where() string { return \"local\" }\n")
	writeFile(t, filepath.Join(dir, "sdk", "fresh", "go.mod"),
		"module github.com/tarantool/tt/sdk/v3/fresh\n\ngo 1.20\n")
	writeFile(t, filepath.Join(dir, "sdk", "testdata", "mod", "go.mod"),
		"module example.com/testdata\n\ngo 1.99\n")
	writeFile(t, filepath.Join(dir, "modules", "aeon", "go.mod"),
		"module github.com/tarantool/tt/modules/aeon\n\ngo 1.21\n")
}

// writeSDKModule writes a tt module of path mod into dir that requires the SDK
// at a commit no proxy has and uses what only the local SDK has.
func writeSDKModule(t *testing.T, dir, mod string) {
	t.Helper()

	writeFile(t, filepath.Join(dir, "go.mod"), "module "+mod+"\n\ngo 1.21\n\n"+
		"require github.com/tarantool/tt/sdk/v3 "+unpushedSDK+"\n")
	writeFile(t, filepath.Join(dir, "mod.go"), `package mod

import "github.com/tarantool/tt/sdk/v3"

// Where tells which SDK the module is built on.
var Where = sdk.Where()
`)
}

// writeDistribution writes tt.manifest.toml into dir with the core and the
// dependencies given.
func writeDistribution(t *testing.T, dir, core, deps string) string {
	t.Helper()

	path := filepath.Join(dir, "tt.manifest.toml")
	writeFile(t, path, "manifest_version = \"0.1\"\nmanifest_kind = \"tt-distribution\"\n\n"+
		core+"\n\n[dependencies]\n"+deps)

	return path
}

// filePaths returns the paths of the files of a result.
func filePaths(result *WorkspaceResult) []string {
	paths := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		paths = append(paths, file.Path)
	}

	return paths
}

// TestWorkspaceLayouts checks where the go.work files go: beside the
// manifest for the modules in its directory - a module with the manifest
// at its root, a distribution with its modules under it - and in the
// directory of a module beside the distribution.
func TestWorkspaceLayouts(t *testing.T) {
	t.Parallel()
	needGo(t)

	root := t.TempDir()
	core := filepath.Join(root, "tt")
	fakeCore(t, core)

	t.Run("module at the root", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(root, "tt-foo")
		writeSDKModule(t, dir, "example.com/tt-foo")

		manifest := writeDistribution(t, dir, "[core]\npath = \"../tt\"", "foo = \".\"\n")

		result, err := Workspace(t.Context(), WorkspaceOptions{Manifest: manifest})
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(dir, "go.work")}, filePaths(result))

		data, err := os.ReadFile(filepath.Join(dir, "go.work"))
		require.NoError(t, err)
		assert.Equal(t, `// Written by tt binary workspace: the local modules of the distribution
// ./tt.manifest.toml on the SDK of the core ../tt,
// whatever their go.mod requires. tt binary workspace --remove deletes it.
// Keep it out of git.

go 1.21

use (
	.
)

replace (
	github.com/tarantool/tt/modules/aeon => ../tt/modules/aeon
	github.com/tarantool/tt/sdk/v3 => ../tt/sdk
	github.com/tarantool/tt/sdk/v3/fresh => ../tt/sdk/fresh
	github.com/tarantool/tt/v3 => ../tt
)
`, string(data))
	})

	t.Run("modules under the distribution and beside it", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(root, "dist")
		writeSDKModule(t, filepath.Join(dir, "plugins", "a"), "example.com/a")
		writeSDKModule(t, filepath.Join(dir, "plugins", "b"), "example.com/b")
		writeSDKModule(t, filepath.Join(root, "tt-c"), "example.com/c")
		writeFile(t, filepath.Join(root, "lib", "go.mod"), "module example.com/lib\n\ngo 1.22\n")

		manifest := writeDistribution(t, dir, "[core]\ntt = \"v3.0.0\"",
			"a = \"./plugins/a\"\nb = \"./plugins/b\"\nc = \"../tt-c\"\n"+
				"registry = \"github.com/acme/tt-registry@v1.0.0\"\n\n"+
				"[replace]\n\"example.com/lib\" = \"../lib\"\n"+
				"\"example.com/other\" = \"example.com/other@v1.0.0\"\n")

		result, err := Workspace(t.Context(), WorkspaceOptions{
			Manifest: manifest, Core: "../tt", CoreDir: dir,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{
			filepath.Join(dir, "go.work"), filepath.Join(root, "tt-c", "go.work"),
		}, filePaths(result))

		data, err := os.ReadFile(filepath.Join(root, "tt-c", "go.work"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "\ngo 1.22\n")
		assert.Contains(t, string(data),
			"// ../dist/tt.manifest.toml on the SDK of the core ../tt,\n")
		assert.Contains(t, string(data),
			"use (\n\t../dist/plugins/a\n\t../dist/plugins/b\n\t.\n)\n")
		assert.Contains(t, string(data), "\texample.com/lib => ../lib\n")
		assert.NotContains(t, string(data), "example.com/other")
		assert.NotContains(t, string(data), "testdata")

		data, err = os.ReadFile(filepath.Join(dir, "go.work"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "use (\n\t./plugins/a\n\t./plugins/b\n\t../tt-c\n)\n")
		assert.Contains(t, string(data), "\tgithub.com/tarantool/tt/sdk/v3 => ../tt/sdk\n")
	})
}

// TestWorkspaceBuildsOnLocalSDK checks the point of the workspace: a module
// that requires the SDK at a commit no proxy has builds, offline, on the
// SDK of the local core.
func TestWorkspaceBuildsOnLocalSDK(t *testing.T) {
	t.Parallel()
	needGo(t)

	root := t.TempDir()
	fakeCore(t, filepath.Join(root, "tt"))

	dir := filepath.Join(root, "tt-foo")
	writeSDKModule(t, dir, "example.com/tt-foo")
	writeFile(t, filepath.Join(dir, "mod_test.go"), `package mod

import "testing"

func TestWhere(t *testing.T) {
	if Where != "local" {
		t.Fatal(Where)
	}
}
`)

	manifest := writeDistribution(t, filepath.Join(root, "dist"), "[core]\npath = \"../tt\"",
		"foo = \"../tt-foo\"\n")

	env := offlineGoEnv(t)

	out, err := goIn(t, dir, env, "build", "./...")
	require.Error(t, err, "without the workspace the unpushed SDK is nowhere: %s", out)

	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Environ: env})
	require.NoError(t, err)

	out, err = goIn(t, dir, env, "list", "-m", "all")
	require.NoError(t, err, out)
	assert.Contains(t, out, "github.com/tarantool/tt/sdk/v3 "+unpushedSDK+" => ../tt/sdk")

	out, err = goIn(t, dir, env, "test", "./...")
	require.NoError(t, err, out)

	// The build ignores the workspace: the content hash of the module is
	// the same with it.
	files, err := moduleFiles(dir, filepath.Join(root, "dist"), "")
	require.NoError(t, err)
	assert.False(t, slices.ContainsFunc(files, func(f moduleFile) bool {
		return f.rel == "go.work" || f.rel == "go.work.sum"
	}))
}

// offlineGoEnv is an environment of the go command that fetches nothing,
// with a module cache of its own and no workspace or flags of the machine.
func offlineGoEnv(t *testing.T) []string {
	t.Helper()

	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOFLAGS=") ||
			strings.HasPrefix(entry, "GOPROXY=") || strings.HasPrefix(entry, "GOMODCACHE=") ||
			strings.HasPrefix(entry, "GOTOOLCHAIN=") || strings.HasPrefix(entry, "GOENV=")
	})

	// An empty variable does not override what go env -w wrote, such as
	// GOFLAGS=-mod=mod: GOENV=off leaves that file out.
	return append(env, "GOENV=off", "GOPROXY=off", "GOFLAGS=", "GOTOOLCHAIN=local",
		"GOMODCACHE="+filepath.Join(t.TempDir(), "modcache"))
}

// goIn runs the go command in dir and returns what it printed.
func goIn(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), goTool, args...)

	cmd.Dir = dir
	cmd.Env = env

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// TestWorkspaceOwnFiles checks that a go.work tt did not write is refused
// unless forced, that one it wrote is rewritten, and that --remove deletes
// only the files it wrote, with their go.work.sum.
func TestWorkspaceOwnFiles(t *testing.T) {
	t.Parallel()
	needGo(t)

	root := t.TempDir()
	fakeCore(t, filepath.Join(root, "tt"))

	dir := filepath.Join(root, "tt-foo")
	writeSDKModule(t, dir, "example.com/tt-foo")

	manifest := writeDistribution(t, dir, "[core]\npath = \"../tt\"", "foo = \".\"\n")
	work := filepath.Join(dir, "go.work")
	foreign := "go 1.21\n\nuse .\n"

	writeFile(t, work, foreign)

	_, err := Workspace(t.Context(), WorkspaceOptions{Manifest: manifest})
	require.ErrorIs(t, err, errForeignWorkspace)

	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Remove: true})
	require.ErrorIs(t, err, errForeignWorkspace)
	assert.FileExists(t, work)

	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Remove: true, Force: true})
	require.ErrorIs(t, err, errWorkspaceRemoveForce)

	result, err := Workspace(t.Context(),
		WorkspaceOptions{Manifest: manifest, Force: true, DryRun: true})
	require.NoError(t, err)
	require.Len(t, result.Files, 1)
	assert.True(t, strings.HasPrefix(string(result.Files[0].Content), workspaceMark))

	data, err := os.ReadFile(work)
	require.NoError(t, err)
	assert.Equal(t, foreign, string(data), "a dry run writes nothing")

	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Force: true})
	require.NoError(t, err)

	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest})
	require.NoError(t, err, "its own go.work is rewritten")

	writeFile(t, filepath.Join(dir, "go.work.sum"), "")

	result, err = Workspace(t.Context(),
		WorkspaceOptions{Manifest: manifest, Remove: true, DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []string{work}, filePaths(result))
	assert.FileExists(t, work)

	result, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Remove: true})
	require.NoError(t, err)
	assert.Equal(t, []string{work}, filePaths(result))
	assert.NoFileExists(t, work)
	assert.NoFileExists(t, filepath.Join(dir, "go.work.sum"))

	result, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Remove: true})
	require.NoError(t, err, "nothing to remove is no error")
	assert.Empty(t, result.Files)
}

// TestWorkspaceRefused checks what a workspace needs: a local core and a
// local module.
func TestWorkspaceRefused(t *testing.T) {
	t.Parallel()
	needGo(t)

	root := t.TempDir()
	fakeCore(t, filepath.Join(root, "tt"))

	dir := filepath.Join(root, "tt-foo")
	writeSDKModule(t, dir, "example.com/tt-foo")

	manifest := writeDistribution(t, dir, "[core]\ntt = \"v3.0.0\"", "foo = \".\"\n")
	_, err := Workspace(t.Context(), WorkspaceOptions{Manifest: manifest})
	require.ErrorContains(t, err, "set [core].path, or give --core DIR")

	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Core: "v3.1.0"})
	require.ErrorContains(t, err, "set [core].path, or give --core DIR")

	manifest = writeDistribution(t, dir, "[core]\npath = \"../tt\"",
		"foo = \"github.com/acme/tt-foo@v1.0.0\"\n")
	_, err = Workspace(t.Context(), WorkspaceOptions{Manifest: manifest})
	require.ErrorContains(t, err, "no module is given by a path")
	assert.NoFileExists(t, filepath.Join(dir, "go.work"))
}
