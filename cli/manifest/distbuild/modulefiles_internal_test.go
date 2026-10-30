package distbuild

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listFiles returns the paths moduleFiles lists for the module in dir,
// sorted.
func listFiles(t *testing.T, dir, manifestDir string) []string {
	t.Helper()

	files, err := moduleFiles(dir, manifestDir, "")
	require.NoError(t, err)

	rels := make([]string, 0, len(files))

	for _, file := range files {
		assert.Equal(t, filepath.Join(dir, filepath.FromSlash(file.rel)), file.path)

		rels = append(rels, file.rel)
	}

	slices.Sort(rels)

	return rels
}

// outsideRepository skips a test whose temporary directory dir is in a git
// repository, whose .gitignore would take part in it.
func outsideRepository(t *testing.T, dir string) {
	t.Helper()

	for parent := dir; filepath.Dir(parent) != parent; parent = filepath.Dir(parent) {
		_, err := os.Lstat(filepath.Join(parent, gitDir))
		if err == nil {
			t.Skipf("the temporary directory is in the git repository %s", parent)
		}
	}
}

// TestModuleFilesLeaveIgnoredOut checks that what .gitignore ignores - a
// binary, a log, a build directory - is no file of the module.
func TestModuleFilesLeaveIgnoredOut(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outsideRepository(t, dir)

	writeFile(t, filepath.Join(dir, ".gitignore"), "# junk\n/tt\n*.log\nbuild/\n\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "m.go"), "package m\n")
	writeFile(t, filepath.Join(dir, "lua", "init.lua"), "return {}\n")
	writeFile(t, filepath.Join(dir, "tt"), "a binary\n")
	writeFile(t, filepath.Join(dir, "debug.log"), "x\n")
	writeFile(t, filepath.Join(dir, "lua", "debug.log"), "x\n")
	writeFile(t, filepath.Join(dir, "build", "out.bin"), "x\n")
	// /tt is anchored: a tt deeper is the module's.
	writeFile(t, filepath.Join(dir, "lua", "tt"), "x\n")

	assert.Equal(t, []string{".gitignore", "go.mod", "lua/init.lua", "lua/tt", "m.go"},
		listFiles(t, dir, dir))
}

// TestModuleFilesNoGitFile checks that the .git file of a worktree, which
// names a directory of the machine, is no file of the module.
func TestModuleFilesNoGitFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, ".git"), "gitdir: /elsewhere/.git/worktrees/m\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")

	assert.Equal(t, []string{"go.mod"}, listFiles(t, dir, dir))
}

// TestModuleFilesNestedIgnore checks that a .gitignore of a subdirectory
// rules its subtree only, after the ones above it.
func TestModuleFilesNestedIgnore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outsideRepository(t, dir)

	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.cache\n")
	writeFile(t, filepath.Join(dir, "a.tmp"), "x\n")
	writeFile(t, filepath.Join(dir, "a.cache"), "x\n")
	writeFile(t, filepath.Join(dir, "sub", ".gitignore"), "*.tmp\n!keep.cache\n")
	writeFile(t, filepath.Join(dir, "sub", "b.tmp"), "x\n")
	writeFile(t, filepath.Join(dir, "sub", "b.cache"), "x\n")
	writeFile(t, filepath.Join(dir, "sub", "keep.cache"), "x\n")
	writeFile(t, filepath.Join(dir, "sub", "deeper", "c.tmp"), "x\n")
	writeFile(t, filepath.Join(dir, "sub", "deeper", "c.lua"), "x\n")

	assert.Equal(t, []string{
		".gitignore", "a.tmp", "go.mod", "sub/.gitignore", "sub/deeper/c.lua", "sub/keep.cache",
	}, listFiles(t, dir, dir))
}

// TestModuleFilesNegation checks ! as git reads it: a later pattern takes
// a file back, but not out of a directory that is ignored.
func TestModuleFilesNegation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outsideRepository(t, dir)

	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, ".gitignore"),
		"*.key\n!root.key\nout/\n!out/keep.txt\n")
	writeFile(t, filepath.Join(dir, "user.key"), "x\n")
	writeFile(t, filepath.Join(dir, "root.key"), "x\n")
	writeFile(t, filepath.Join(dir, "certs", "root.key"), "x\n")
	writeFile(t, filepath.Join(dir, "out", "keep.txt"), "x\n")

	assert.Equal(t, []string{".gitignore", "certs/root.key", "go.mod", "root.key"},
		listFiles(t, dir, dir))
}

// TestModuleFilesRepositoryIgnore checks that the .gitignore files above a
// module rule it up to the root of its repository, and no further.
func TestModuleFilesRepositoryIgnore(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		// git makes the root of the repository: a .git directory, or the
		// .git file of a worktree.
		git func(t *testing.T, repo string)
	}{
		{"directory", func(t *testing.T, repo string) {
			t.Helper()
			writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/x\n")
		}},
		{"worktree", func(t *testing.T, repo string) {
			t.Helper()
			writeFile(t, filepath.Join(repo, ".git"), "gitdir: /elsewhere\n")
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			outer := t.TempDir()
			repo := filepath.Join(outer, "repo")
			mod := filepath.Join(repo, "plugins", "m")
			// The manifest is elsewhere in the repository.
			manifestDir := filepath.Join(repo, "dist")

			testCase.git(t, repo)
			// Above the repository: not its rules.
			writeFile(t, filepath.Join(outer, ".gitignore"), "*.lua\n")
			writeFile(t, filepath.Join(repo, ".gitignore"), "*.junk\n/plugins/m/local.cfg\n")
			writeFile(t, filepath.Join(repo, "plugins", ".gitignore"), "/m/tmp/\n")
			writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/m\n")
			writeFile(t, filepath.Join(mod, "init.lua"), "return {}\n")
			writeFile(t, filepath.Join(mod, "x.junk"), "x\n")
			writeFile(t, filepath.Join(mod, "local.cfg"), "x\n")
			writeFile(t, filepath.Join(mod, "tmp", "state"), "x\n")

			assert.Equal(t, []string{"go.mod", "init.lua"}, listFiles(t, mod, manifestDir))
		})
	}
}

// TestModuleFilesIgnoredModule checks that a module in a directory its
// repository ignores is listed as an ignored directory is: the files the
// build may need only.
func TestModuleFilesIgnoredModule(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	mod := filepath.Join(repo, "third_party", "m")

	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/x\n")
	writeFile(t, filepath.Join(repo, ".gitignore"), "/third_party/\n")
	writeFile(t, filepath.Join(mod, ".gitignore"), "!*.lua\n")
	writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(mod, "m.go"), "package m\n")
	writeFile(t, filepath.Join(mod, "init.lua"), "return {}\n")

	assert.Equal(t, []string{"go.mod", "m.go"}, listFiles(t, mod, repo))
}

// TestModuleFilesManifestIgnore checks that outside a repository the
// .gitignore files above a module rule it up to the directory of the
// manifest when the module is in it, and none rule it when it is not.
func TestModuleFilesManifestIgnore(t *testing.T) {
	t.Parallel()

	outer := t.TempDir()
	outsideRepository(t, outer)

	project := filepath.Join(outer, "project")
	inside := filepath.Join(project, "plugins", "m")
	outside := filepath.Join(outer, "libs", "m")

	writeFile(t, filepath.Join(outer, ".gitignore"), "*.lua\n")
	writeFile(t, filepath.Join(project, ".gitignore"), "*.junk\n")
	writeFile(t, filepath.Join(outer, "libs", ".gitignore"), "*.junk\n")

	for _, mod := range []string{inside, outside} {
		writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/m\n")
		writeFile(t, filepath.Join(mod, "init.lua"), "return {}\n")
		writeFile(t, filepath.Join(mod, "x.junk"), "x\n")
	}

	assert.Equal(t, []string{"go.mod", "init.lua"}, listFiles(t, inside, project))
	assert.Equal(t, []string{"go.mod", "init.lua", "x.junk"}, listFiles(t, outside, project))
}

// TestModuleFilesNoMachineExcludes checks that .git/info/exclude, which
// each clone has its own of, ignores nothing.
func TestModuleFilesNoMachineExcludes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, ".git", "info", "exclude"), "*.lua\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "init.lua"), "return {}\n")

	assert.Equal(t, []string{"go.mod", "init.lua"}, listFiles(t, dir, dir))
}

// embedModule is the main package of a module that embeds ignored files: a
// file, a directory, a directory with all:.
const embedModule = `package m

import "embed"

//go:embed assets/greeting.txt
var greeting string

//go:embed static
var static embed.FS

//go:embed all:hidden
var hidden embed.FS
`

// TestModuleFilesBuildFiles checks that the files the build may need are
// listed even when .gitignore ignores them: the sources, in an ignored
// directory too, and what the packages embed under any build constraint,
// but not what their tests do.
func TestModuleFilesBuildFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outsideRepository(t, dir)

	writeFile(t, filepath.Join(dir, ".gitignore"),
		"/gen/\n/assets/\n/static/\n/hidden/\nzz_generated.go\n*.bin\n*.txt\ngo.sum\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "go.sum"), "")
	writeFile(t, filepath.Join(dir, "m.go"), embedModule)
	writeFile(t, filepath.Join(dir, "zz_generated.go"), "package m\n")
	writeFile(t, filepath.Join(dir, "win.go"), "//go:build windows\n\n"+
		"package m\n\nimport _ \"embed\"\n\n//go:embed win.txt\nvar win string\n")
	writeFile(t, filepath.Join(dir, "win.txt"), "x\n")
	writeFile(t, filepath.Join(dir, "m_test.go"),
		"package m\n\nimport _ \"embed\"\n\n//go:embed testdata/fixture.bin\nvar fixture []byte\n")
	writeFile(t, filepath.Join(dir, "testdata", "fixture.bin"), "x\n")
	writeFile(t, filepath.Join(dir, "junk.bin"), "x\n")
	writeFile(t, filepath.Join(dir, "assets", "greeting.txt"), "hello\n")
	writeFile(t, filepath.Join(dir, "assets", "other.txt"), "x\n")
	writeFile(t, filepath.Join(dir, "static", "a.css"), "x\n")
	writeFile(t, filepath.Join(dir, "static", ".hidden.css"), "x\n")
	writeFile(t, filepath.Join(dir, "static", "_draft.css"), "x\n")
	writeFile(t, filepath.Join(dir, "static", "nested", "go.mod"), "module example.com/n\n")
	writeFile(t, filepath.Join(dir, "static", "nested", "n.css"), "x\n")
	writeFile(t, filepath.Join(dir, "hidden", ".dot"), "x\n")
	writeFile(t, filepath.Join(dir, "hidden", "_under"), "x\n")
	writeFile(t, filepath.Join(dir, "gen", "gen.go"),
		"package gen\n\nimport _ \"embed\"\n\n//go:embed tmpl.txt\nvar tmpl string\n")
	writeFile(t, filepath.Join(dir, "gen", "tmpl.txt"), "x\n")
	writeFile(t, filepath.Join(dir, "gen", "gen.h"), "x\n")
	writeFile(t, filepath.Join(dir, "gen", "data.json"), "x\n")

	assert.Equal(t, []string{
		".gitignore",
		"assets/greeting.txt",
		"gen/gen.go",
		"gen/gen.h",
		"gen/tmpl.txt",
		"go.mod",
		"go.sum",
		"hidden/.dot",
		"hidden/_under",
		"m.go",
		"m_test.go",
		"static/a.css",
		"win.go",
		"win.txt",
		"zz_generated.go",
	}, listFiles(t, dir, dir))
}

// TestBundleCopyHashesTheSame checks that the copy of a module in a bundle
// holds the files its content hash covers: it hashes the same as the
// module, without what .gitignore ignores and with what the build embeds.
func TestBundleCopyHashesTheSame(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	mod := filepath.Join(repo, "plugins", "m")
	work := t.TempDir()

	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/x\n")
	writeFile(t, filepath.Join(repo, ".gitignore"), "*.junk\n!keep.junk\n")
	writeFile(t, filepath.Join(mod, ".gitignore"), "/assets/\n/static/\n/hidden/\n")
	writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(mod, "m.go"), embedModule)
	writeFile(t, filepath.Join(mod, "init.lua"), "return {}\n")
	writeFile(t, filepath.Join(mod, "x.junk"), "x\n")
	writeFile(t, filepath.Join(mod, "keep.junk"), "x\n")
	writeFile(t, filepath.Join(mod, "assets", "greeting.txt"), "hello\n")
	writeFile(t, filepath.Join(mod, "assets", "other.txt"), "x\n")
	writeFile(t, filepath.Join(mod, "static", "a.css"), "x\n")
	writeFile(t, filepath.Join(mod, "hidden", ".dot"), "x\n")

	bld := builder{dir: repo, work: work}
	dst := filepath.Join(work, "local", "modules", "m")

	require.NoError(t, bld.copyModule(mod, dst))

	assert.FileExists(t, filepath.Join(dst, "assets", "greeting.txt"))
	assert.FileExists(t, filepath.Join(dst, "keep.junk"))
	assert.NoFileExists(t, filepath.Join(dst, "x.junk"))
	assert.NoFileExists(t, filepath.Join(dst, "assets", "other.txt"))

	want, err := contentHash([]hashRoot{{dir: mod, prefix: ""}}, repo, work)
	require.NoError(t, err)

	got, err := contentHash([]hashRoot{{dir: dst, prefix: ""}}, dst, "")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, listFiles(t, mod, repo), listFiles(t, dst, dst))
}
