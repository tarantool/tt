package emmylua_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/emmylua"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// isolateGit keeps the configuration of the user and of the system from the
// git the test runs, so that no excludesFile of theirs hides a file, and
// keeps git from finding a repository above ceiling: a temporary directory
// out of every work tree stays out of one wherever the tests run.
func isolateGit(t *testing.T, ceiling string) {
	t.Helper()

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
}

// git runs git with args in dir.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", append([]string{
		"-C", dir, "-c", "user.name=tt", "-c", "user.email=tt@example.org",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=master",
	}, args...)...)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

// needGit skips the test when git is not installed.
func needGit(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
}

// gitProject returns a project in a git work tree holding Lua files git
// tracks, untracked ones, ignored ones, by .gitignore and by
// .git/info/exclude, and those luafmt's walk does not take, with luafmt in
// its rocks running script.
func gitProject(t *testing.T, script string) string {
	t.Helper()

	project := testProject(t)
	isolateGit(t, filepath.Dir(project))
	git(t, project, "init", "-q")

	for _, file := range []string{
		"app/init.lua", "app/lib/util.lua",
		// tt's own directories, tracked or not, and what luafmt does not
		// walk into.
		"_build/rock/app/init.lua", ".hidden/tool.lua", "node_modules/m/init.lua",
		// Gone from the work tree, still in the index.
		"gone.lua",
		"README.md",
	} {
		writeFile(t, filepath.Join(project, file), "return {}\n")
	}

	require.NoError(t, os.Symlink("app/init.lua", filepath.Join(project, "link.lua")))

	// A repository of its own, a gitlink in the index as a submodule is.
	sub := filepath.Join(project, "vendor", "lib")
	writeFile(t, filepath.Join(sub, "lib.lua"), "return {}\n")
	git(t, sub, "init", "-q")
	git(t, sub, "add", ".")
	git(t, sub, "commit", "-q", "-m", "lib")

	writeFile(t, filepath.Join(project, ".gitignore"), "ignored.lua\nscratch/\n")
	git(t, project, "add", ".")
	git(t, project, "commit", "-q", "-m", "app")
	require.NoError(t, os.Remove(filepath.Join(project, "gone.lua")))

	writeFile(t, filepath.Join(project, "untracked.lua"), "return {}\n")
	writeFile(t, filepath.Join(project, "notes.txt"), "draft\n")
	writeFile(t, filepath.Join(project, "ignored.lua"), "return {}\n")
	writeFile(t, filepath.Join(project, "scratch", "draft.lua"), "return {}\n")
	writeFile(t, filepath.Join(project, "_build", "rock", "app", "lib.lua"), "return {}\n")
	writeFile(t, filepath.Join(project, ".git", "info", "exclude"), "local.lua\n")
	writeFile(t, filepath.Join(project, "local.lua"), "return {}\n")

	writeProgram(t, project, "luafmt", script)

	return project
}

// TestFmtGit checks that tt emmylua fmt with no path, in a git work tree,
// gives luafmt the Lua files git sees: tracked and untracked but not
// ignored, without _build/ even tracked, a submodule, a file gone from the
// work tree, and those luafmt's walk would not take - hidden, under
// node_modules/, a link - unless the flags after -- make it take them. A path
// given, or a flag after -- that formats no files, works as out of git. It
// runs programs, so it is not parallel, as TestCheck.
func TestFmtGit(t *testing.T) {
	needGit(t)

	project := gitProject(t, "echo \"$@\"\n")

	const (
		level = "--level lua-jit "
		files = "app/init.lua app/lib/util.lua untracked.lua"
	)

	run := func(cases []fmtCase) {
		t.Helper()

		for _, testCase := range cases {
			got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
				append([]string{"emmylua", "fmt"}, testCase.args...)...)
			require.NoError(t, got.Err, testCase.args)
			assert.Equal(t, testCase.want+"\n", got.Stdout, testCase.args)
		}
	}

	run([]fmtCase{
		{args: nil, want: "--check " + level + files},
		{args: []string{"--write"}, want: "--write " + level + files},
		{args: []string{"--", "--list-different"}, want: level + "--list-different " + files},
		{
			args: []string{"--", "--include-hidden"},
			want: "--check " + level + "--include-hidden .hidden/tool.lua " + files,
		},
		{
			args: []string{"--", "--follow-symlinks"},
			want: "--check " + level + "--follow-symlinks " +
				"app/init.lua app/lib/util.lua link.lua untracked.lua",
		},
		{args: []string{"--", "--level", "lua51"}, want: "--check --level lua51 " + files},
		// A path given is the user's, as out of git.
		{args: []string{"."}, want: "--check " + level + "."},
		{args: []string{"scratch"}, want: "--check " + level + "scratch"},
		// A flag that formats no files takes the root, as out of git.
		{
			args: []string{"--", "--init=luafmt.toml"},
			want: level + "--exclude _build/** --init=luafmt.toml " + project,
		},
	})

	// luafmt runs in the root, so a relative --config is made absolute.
	writeFile(t, filepath.Join(project, "style.json"), `{"indent": {"width": 2}}`)
	run([]fmtCase{{
		args: []string{"--", "--config", "style.json"},
		want: "--check " + level + "--config " + filepath.Join(project, "style.json") + " " + files,
	}})

	// The files go in runs of the budget, each with a file at least.
	emmylua.SetFmtArgBudget(t, len("app/init.lua")+1)
	run([]fmtCase{{
		args: nil,
		want: "--check " + level + "app/init.lua\n" +
			"--check " + level + "app/lib/util.lua\n" +
			"--check " + level + "untracked.lua",
	}})
	emmylua.SetFmtArgBudget(t, len("app/init.lua app/lib/util.lua")+1)
	run([]fmtCase{{
		args: nil,
		want: "--check " + level + "app/init.lua app/lib/util.lua\n" +
			"--check " + level + "untracked.lua",
	}})

	// The files whose configuration sets the level run without --level.
	writeFile(t, filepath.Join(project, "app", "luafmt.toml"), "[syntax]\nlevel = \"Lua51\"\n")
	run([]fmtCase{{
		args: nil,
		want: "--check " + level + "untracked.lua\n--check app/init.lua app/lib/util.lua",
	}})
}

// TestFmtGitRuns checks that tt emmylua fmt makes every run of luafmt on the
// files git sees, in the root of the project wherever it is run, and fails
// as the run that exits with the highest code. It runs programs, so it is
// not parallel, as TestCheck.
func TestFmtGitRuns(t *testing.T) {
	needGit(t)

	project := gitProject(t, "echo \"$(pwd -P): $@\"\n"+
		"case \"$*\" in *util*) exit 2;; esac\nexit 1\n")
	emmylua.SetFmtArgBudget(t, 1)

	root, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)

	sub := filepath.Join(project, "app", "lib")

	got := sdktest.New(t, sdktest.WithProject(sub)).Run(emmylua.New, "emmylua", "fmt")
	require.ErrorContains(t, got.Err, "luafmt failed")
	assert.True(t, sdk.IsReported(got.Err))
	assert.Equal(t, 2, got.ExitCode())
	assert.Equal(t, root+": --check --level lua-jit app/init.lua\n"+
		root+": --check --level lua-jit app/lib/util.lua\n"+
		root+": --check --level lua-jit untracked.lua\n", got.Stdout)
}

// TestFmtGitNoFiles checks that tt emmylua fmt with no path, in a git work
// tree with no Lua file git sees, runs no luafmt, which would read stdin,
// and succeeds. It runs programs, so it is not parallel, as TestCheck.
func TestFmtGitNoFiles(t *testing.T) {
	needGit(t)

	project := testProject(t)
	isolateGit(t, filepath.Dir(project))
	git(t, project, "init", "-q")
	writeFile(t, filepath.Join(project, ".gitignore"), "draft.lua\n")
	writeFile(t, filepath.Join(project, "draft.lua"), "return {}\n")
	writeProgram(t, project, "luafmt", "echo \"$@\"\n")

	services := sdktest.New(t, sdktest.WithProject(project))
	got := services.Run(emmylua.New, "emmylua", "fmt")
	require.NoError(t, got.Err)
	assert.Empty(t, got.Stdout)
	assert.Contains(t, infoMessages(services),
		"git sees no Lua files in "+project+": nothing to format")
}

// TestFmtGitIgnoredProject checks that tt emmylua fmt with no path in a
// project git ignores, in a work tree, walks the project as out of git: git
// would see none of its files. It runs programs, so it is not parallel, as
// TestCheck.
func TestFmtGitIgnoredProject(t *testing.T) {
	needGit(t)

	outer := t.TempDir()
	isolateGit(t, filepath.Dir(outer))
	git(t, outer, "init", "-q")
	writeFile(t, filepath.Join(outer, ".gitignore"), "work/\n")

	project := filepath.Join(outer, "work")
	writeFile(t, filepath.Join(project, "app.manifest.toml"), "manifest_version = \"0.1\"\n")
	writeFile(t, filepath.Join(project, "init.lua"), "return {}\n")
	writeProgram(t, project, "luafmt", "echo \"$@\"\n")

	got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New, "emmylua", "fmt")
	require.NoError(t, got.Err)
	assert.Equal(t, "--check --level lua-jit --exclude _build/** "+project+"\n", got.Stdout)
}
