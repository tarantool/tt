package emmylua_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/emmylua"
	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/sdktest"
)

// noTarantool is the warning of a command that finds no Tarantool.
const noTarantool = "no Tarantool found: checking without the types of the Tarantool API"

// echoArgs is a program that prints its arguments and exits with 1.
const echoArgs = "echo \"$@\"\nexit 1\n"

// writeProgram writes a shell script running the commands of script as
// program in the rocks of project, where tt emmylua prepare installs the
// programs.
func writeProgram(t *testing.T, project, program, script string) {
	t.Helper()

	bin := filepath.Join(project, ".rocks", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, program),
		[]byte("#!/bin/sh\n"+script), 0o755))
}

// warnings returns the messages of the warnings in records.
func warnings(records []logtest.Record) []string {
	return messagesAt(records, slog.LevelWarn)
}

// debugs returns the messages of the debug records in records.
func debugs(records []logtest.Record) []string {
	return messagesAt(records, slog.LevelDebug)
}

// messagesAt returns the messages of the records of level in records.
func messagesAt(records []logtest.Record, level slog.Level) []string {
	var messages []string

	for _, record := range records {
		if record.Level == level {
			messages = append(messages, record.Message)
		}
	}

	return messages
}

// writeFile writes content to path, making its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// configPath is the configuration tt writes for EmmyLua in project.
func configPath(project string) string {
	return filepath.Join(project, ".tarantool", "emmylua", "emmyrc.json")
}

// TestCheck checks that tt emmylua check runs emmylua_check of the rocks of
// the project with the configuration it writes, then those of the project,
// the arguments after -- and the paths, the project when there are none, and
// fails as the program does: with its exit code and nothing more to report.
// It runs programs, so it is not parallel: a fork elsewhere could hold the
// script it has just written open.
func TestCheck(t *testing.T) {
	project := testProject(t)
	writeProgram(t, project, "emmylua_check", echoArgs)

	got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New, "emmylua", "check")
	require.ErrorContains(t, got.Err, "emmylua_check failed")
	assert.True(t, sdk.IsReported(got.Err))
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
	assert.Equal(t, "--config "+configPath(project)+" "+project+"\n", got.Stdout)
	assert.Equal(t, []string{noTarantool}, warnings(got.Records))
	assert.FileExists(t, configPath(project))

	projectConfig := filepath.Join(project, ".emmyrc.json")
	require.NoError(t, os.WriteFile(projectConfig, []byte("{}"), 0o644))

	got = sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "check", "--", "--warnings-as-errors")
	assert.True(t, sdk.IsReported(got.Err))
	assert.Equal(t, "--config "+configPath(project)+" --config "+projectConfig+
		" --warnings-as-errors "+project+"\n", got.Stdout)

	// The frequent flags need no --, and go before what follows it.
	got = sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "check", "--warnings-as-errors", "-f", "json", "--severity", "warn",
		"--", "--output", "report.json")
	assert.True(t, sdk.IsReported(got.Err))
	assert.Equal(t, "--config "+configPath(project)+" --config "+projectConfig+
		" --warnings-as-errors --output-format json --severity warn --output report.json "+
		project+"\n", got.Stdout)
}

// TestCheckPaths checks that the paths given to tt emmylua check narrow the
// run on the root of the project: a configuration after the one tt writes
// leaves every other entry of the project out of the workspace and reads
// them, but .rocks/, .tarantool/ and _build/, as a library of links laid
// out as in the project, and is removed after the run. The root itself
// narrows nothing, and a path out of the project is refused. It runs
// programs, so it is not parallel, as TestCheck.
func TestCheckPaths(t *testing.T) {
	project := testProject(t)
	// The program prints its arguments, then the narrowing, the second
	// configuration, with the links of its library.
	writeProgram(t, project, "emmylua_check", "echo \"$@\"\n"+
		"cat \"$4\"\nlib=\"$(dirname \"$4\")/lib\"\n"+
		"cd \"$lib\" && find . -type l -exec sh -c 'echo \"$1 -> $(readlink \"$1\")\"' _ {} \\; "+
		"| sort\n")

	for _, dir := range []string{"app/lib", "app/util", "spec", ".rocks", "_build"} {
		require.NoError(t, os.MkdirAll(filepath.Join(project, dir), 0o755))
	}

	writeFile(t, filepath.Join(project, "init.lua"), "return {}\n")
	writeFile(t, filepath.Join(project, "app", "lib", "store.lua"), "return {}\n")
	writeFile(t, filepath.Join(project, "app", "init.lua"), "return {}\n")

	sub := filepath.Join(project, "app")

	got := sdktest.New(t, sdktest.WithProject(sub)).Run(emmylua.New,
		"emmylua", "check", "lib/store.lua", "-f", "json")
	require.NoError(t, got.Err)

	lines := strings.SplitN(got.Stdout, "\n", 2)
	require.Len(t, lines, 2)

	argv := strings.Fields(lines[0])
	require.Len(t, argv, 7)
	assert.Equal(t, []string{"--config", configPath(project), "--config"}, argv[:3])
	assert.Equal(t, []string{"--output-format", "json", project}, argv[4:])

	narrowing := argv[3]
	assert.Equal(t, filepath.Dir(configPath(project)), filepath.Dir(filepath.Dir(narrowing)))
	assert.NoFileExists(t, narrowing)

	// The configuration ends with the } that closes it, alone on its line.
	config, links, found := strings.Cut(lines[1], "\n}\n")
	require.True(t, found)

	var workspace struct {
		Workspace struct {
			IgnoreDir []string `json:"ignoreDir"` //nolint:tagliatelle // EmmyLua's name.
			Library   []string `json:"library"`
		} `json:"workspace"`
	}

	require.NoError(t, json.Unmarshal([]byte(config+"\n}"), &workspace))

	root, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)

	inRoot := func(name string) string {
		return filepath.Join(root, filepath.FromSlash(name))
	}

	ignored := make([]string, 0, 8)
	for _, name := range []string{".rocks", ".tarantool", "_build", "app.manifest.toml",
		"init.lua", "spec", "app/init.lua", "app/util"} {
		ignored = append(ignored, inRoot(name))
	}

	assert.ElementsMatch(t, ignored, workspace.Workspace.IgnoreDir)
	assert.Equal(t, []string{filepath.Join(filepath.Dir(narrowing), "lib")},
		workspace.Workspace.Library)
	assert.Equal(t, "./app.manifest.toml -> "+inRoot("app.manifest.toml")+"\n"+
		"./app/init.lua -> "+inRoot("app/init.lua")+"\n"+
		"./app/util -> "+inRoot("app/util")+"\n"+
		"./init.lua -> "+inRoot("init.lua")+"\n"+
		"./spec -> "+inRoot("spec")+"\n", links)

	// The root narrows nothing.
	writeProgram(t, project, "emmylua_check", "echo \"$@\"\n")

	got = sdktest.New(t, sdktest.WithProject(sub)).Run(emmylua.New, "emmylua", "check", "..")
	require.NoError(t, got.Err)
	assert.Equal(t, "--config "+configPath(project)+" "+project+"\n", got.Stdout)

	got = sdktest.New(t, sdktest.WithProject(sub)).Run(emmylua.New,
		"emmylua", "check", t.TempDir())
	require.ErrorIs(t, got.Err, analyzer.ErrOutside)

	got = sdktest.New(t, sdktest.WithProject(sub)).Run(emmylua.New,
		"emmylua", "check", "missing.lua")
	require.ErrorContains(t, got.Err, "missing.lua")
}

// TestCheckInSubdirectory checks that tt emmylua check run in a subdirectory
// of a project works on the project, as the project commands of tt do: the
// program of its rocks, its configuration, the project when no path is
// given. It runs programs, so it is not parallel, as TestCheck.
func TestCheckInSubdirectory(t *testing.T) {
	project := testProject(t)
	writeProgram(t, project, "emmylua_check", "echo \"$@\"\n")

	sub := filepath.Join(project, "app", "lib")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	services := sdktest.New(t, sdktest.WithProject(sub))
	got := services.Run(emmylua.New, "emmylua", "check")
	require.NoError(t, got.Err)
	assert.Equal(t, "--config "+configPath(project)+" "+project+"\n", got.Stdout)
	assert.Contains(t, infoMessages(services), "working on the project in "+project)
	assert.NoDirExists(t, filepath.Join(sub, ".tarantool"))

	// In the root of the project, there is nothing to say.
	services = sdktest.New(t, sdktest.WithProject(project))
	got = services.Run(emmylua.New, "emmylua", "check")
	require.NoError(t, got.Err)
	assert.Empty(t, infoMessages(services))
}

// infoMessages returns the messages services logged at the info level.
func infoMessages(services *sdktest.Services) []string {
	var messages []string

	for _, record := range services.Records() {
		if record.Level == slog.LevelInfo {
			messages = append(messages, record.Message)
		}
	}

	return messages
}

// TestDoc checks that tt emmylua doc passes its flags to emmylua_doc_cli
// with the configuration, the arguments after -- and the paths. It runs
// programs, so it is not parallel, as TestCheck.
func TestDoc(t *testing.T) {
	project := testProject(t)
	writeProgram(t, project, "emmylua_doc_cli", "echo \"$@\"\n")

	got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "doc", "-o", "doc/api", "--output-format", "markdown",
		"--", "--site-name", "App")
	require.NoError(t, got.Err)
	assert.Equal(t, "--config "+configPath(project)+
		" --output doc/api --output-format markdown --site-name App "+project+"\n", got.Stdout)
}

// fmtCase is a run of tt emmylua fmt with args, and the arguments of luafmt
// it wants.
type fmtCase struct {
	args []string
	want string
}

// TestFmt checks that tt emmylua fmt runs luafmt with its mode - --check
// unless --write or a flag after -- chooses another - LuaJIT unless the
// arguments after -- or the configuration of luafmt choose the level, the
// arguments after -- and the paths, without the configuration of the
// analyzer, and succeeds as the program does. On the project, with no path
// given, out of a git work tree (TestFmtGit), it walks the project but
// _build/. It runs programs, so it is not parallel, as TestCheck.
func TestFmt(t *testing.T) {
	project := testProject(t)
	// Out of every work tree, wherever the tests run.
	isolateGit(t, filepath.Dir(project))
	writeProgram(t, project, "luafmt", "echo \"$@\"\n")

	const (
		level   = "--level lua-jit "
		noBuild = "--exclude _build/** "
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
		{args: nil, want: "--check " + level + noBuild + project},
		{args: []string{"app"}, want: "--check " + level + "app"},
		{args: []string{"app", "--check"}, want: "--check " + level + "app"},
		{args: []string{"app", "--write"}, want: "--write " + level + "app"},
		{args: []string{"app", "--", "--check"}, want: level + "--check app"},
		{args: []string{"--write"}, want: "--write " + level + noBuild + project},
		{
			args: []string{"--", "--list-different"},
			want: level + noBuild + "--list-different " + project,
		},
		// A flag that formats no files takes no list of them, and --init
		// with no file writes the one of the root.
		{args: []string{"--", "--init=luafmt.toml"}, want: level + "--init=luafmt.toml"},
		{
			args: []string{"--", "--init", "--tab"},
			want: level + "--init " + filepath.Join(project, "luafmt.toml") + " --tab",
		},
		{args: []string{"--", "--dump-default-config"}, want: level + "--dump-default-config"},
		{
			args: []string{"--", "--explain-config", "app/init.lua"},
			want: level + "--explain-config app/init.lua",
		},
		{args: []string{"app", "--", "--stdin"}, want: level + "--stdin app"},
		{args: []string{"--", "--tab"}, want: "--check " + level + noBuild + "--tab " + project},
		// A path given is the user's: _build/ too.
		{args: []string{"_build/rock"}, want: "--check " + level + "_build/rock"},
		// A level after -- is the user's.
		{
			args: []string{"--", "--level", "lua51"},
			want: "--check " + noBuild + "--level lua51 " + project,
		},
		{args: []string{"app", "--", "--level=lua54"}, want: "--check --level=lua54 app"},
	})

	// A configuration that sets no level leaves it to tt; one that sets it,
	// the one luafmt reads for the path, wins over tt.
	writeFile(t, filepath.Join(project, ".luafmt.toml"), "[indent]\nwidth = 2\n")
	writeFile(t, filepath.Join(project, "app", "luafmt.toml"), "[syntax]\nlevel = \"Lua51\"\n")
	run([]fmtCase{
		{args: nil, want: "--check " + level + noBuild + project},
		{args: []string{"app"}, want: "--check app"},
		{args: []string{"app/init.lua", "lib"}, want: "--check app/init.lua lib"},
		{args: []string{"lib"}, want: "--check " + level + "lib"},
	})

	writeFile(t, filepath.Join(project, ".luafmt.toml"), "syntax.level = \"LuaJIT\"\n")
	writeFile(t, filepath.Join(project, "style.json"), `{"indent": {"width": 2}}`)
	run([]fmtCase{
		{args: nil, want: "--check " + noBuild + project},
		// --config is the configuration of every file.
		{
			args: []string{"--", "--config", "style.json"},
			want: "--check " + level + noBuild + "--config style.json " + project,
		},
		{
			args: []string{"app", "--", "--config=.luafmt.toml"},
			want: "--check --config=.luafmt.toml app",
		},
	})

	assert.NoFileExists(t, configPath(project))

	got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "fmt", "--check", "--write")
	require.ErrorContains(t, got.Err, "none of the others can be")
}

// TestFmtInit checks that tt emmylua fmt -- --init sets the level of LuaJIT
// in the configuration luafmt writes, which names that of Lua 5.5, and says
// so, unless a level follows --. It runs programs, so it is not parallel,
// as TestCheck.
func TestFmtInit(t *testing.T) {
	project := testProject(t)
	isolateGit(t, filepath.Dir(project))
	// luafmt --init FILE, as it writes the level.
	writeProgram(t, project, "luafmt", "while [ $# -gt 0 ]; do\n"+
		"  if [ \"$1\" = --init ]; then printf '[syntax]\\nlevel = \"Lua55\"\\n' > \"$2\"; fi\n"+
		"  shift\ndone\n")

	config := filepath.Join(project, "luafmt.toml")

	services := sdktest.New(t, sdktest.WithProject(project))
	got := services.Run(emmylua.New, "emmylua", "fmt", "--", "--init")
	require.NoError(t, got.Err)

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, "[syntax]\nlevel = \"LuaJIT\"\n", string(data))
	assert.Contains(t, infoMessages(services), "set [syntax] level = \"LuaJIT\" in "+config+
		", the Lua of Tarantool, for \"Lua55\" luafmt writes")

	sub := filepath.Join(project, "app")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	// The test runs luafmt in its own directory, not in sub: the file is
	// named whole.
	style := filepath.Join(sub, "style.toml")

	got = sdktest.New(t, sdktest.WithProject(sub)).Run(emmylua.New,
		"emmylua", "fmt", "--", "--init", style, "--level", "lua54")
	require.NoError(t, got.Err)

	data, err = os.ReadFile(style)
	require.NoError(t, err)
	assert.Equal(t, "[syntax]\nlevel = \"Lua55\"\n", string(data))
}

// TestConfig checks that tt emmylua config writes the configuration, valid
// JSON with the stubs of the Tarantool of the project among its libraries,
// prints its path, and warns of a Tarantool without stubs and of none.
func TestConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stubs := filepath.Join(root, "share", "tarantool", "emmylua")
	require.NoError(t, os.MkdirAll(stubs, 0o755))

	project := testProject(t)
	services := sdktest.New(t, sdktest.WithProject(project),
		sdktest.WithTarantool(filepath.Join(root, "bin", "tarantool"),
			sdk.TarantoolVersion{Major: 3, Minor: 9}))

	got := services.Run(emmylua.New, "emmylua", "config")
	require.NoError(t, got.Err)
	assert.Equal(t, configPath(project)+"\n", got.Stdout)
	assert.Empty(t, warnings(got.Records))

	data, err := os.ReadFile(configPath(project))
	require.NoError(t, err)

	var config struct {
		Runtime struct {
			Version string `json:"version"`
		} `json:"runtime"`
		Workspace struct {
			Library []string `json:"library"`
		} `json:"workspace"`
	}

	require.NoError(t, json.Unmarshal(data, &config))
	assert.Equal(t, "LuaJIT", config.Runtime.Version)
	assert.Equal(t, []string{stubs}, config.Workspace.Library)

	old := filepath.Join(t.TempDir(), "bin", "tarantool")

	got = sdktest.New(t, sdktest.WithProject(project),
		sdktest.WithTarantool(old, sdk.TarantoolVersion{Major: 3})).Run(
		emmylua.New, "emmylua", "config")
	require.NoError(t, got.Err)
	assert.Equal(t, []string{"Tarantool 3.0.0 has no EmmyLua stubs: " +
		"checking without the types of the Tarantool API"}, warnings(got.Records))
	assert.Contains(t, debugs(got.Records), "no share/tarantool/emmylua next to "+old+
		" or the file it links to: Tarantool 3.9 and newer install the stubs there",
		"the path is told with -V")

	got = sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New, "emmylua", "config")
	require.NoError(t, got.Err)
	assert.Equal(t, []string{noTarantool}, warnings(got.Records))
}

// TestNotPrepared checks that a command whose program the rocks of the
// project do not have says to run tt emmylua prepare.
func TestNotPrepared(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"check", "doc", "fmt", "ls"} {
		got := sdktest.New(t, sdktest.WithProject(testProject(t))).Run(emmylua.New,
			"emmylua", command)
		require.ErrorIs(t, got.Err, analyzer.ErrNotPrepared, command)
		require.ErrorContains(t, got.Err, "run tt emmylua prepare", command)
		assert.False(t, sdk.IsReported(got.Err), command)
	}
}

// TestLsArgs checks that tt emmylua ls takes the arguments of emmylua_ls
// after -- only.
func TestLsArgs(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"app"}, {"app", "--", "--log-level", "debug"}} {
		got := sdktest.Run(t, emmylua.New, append([]string{"emmylua", "ls"}, args...)...)
		require.ErrorContains(t, got.Err, "give the arguments of emmylua_ls after --",
			strings.Join(args, " "))
	}
}
