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
	var messages []string

	for _, record := range records {
		if record.Level == slog.LevelWarn {
			messages = append(messages, record.Message)
		}
	}

	return messages
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
	project := t.TempDir()
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
		"emmylua", "check", "app", "spec", "--", "--warnings-as-errors")
	assert.True(t, sdk.IsReported(got.Err))
	assert.Equal(t, "--config "+configPath(project)+" --config "+projectConfig+
		" --warnings-as-errors app spec\n", got.Stdout)

	// The frequent flags need no --, and go before what follows it.
	got = sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "check", "app", "--warnings-as-errors", "-f", "json", "--severity", "warn",
		"--", "--output", "report.json")
	assert.True(t, sdk.IsReported(got.Err))
	assert.Equal(t, "--config "+configPath(project)+" --config "+projectConfig+
		" --warnings-as-errors --output-format json --severity warn --output report.json app\n",
		got.Stdout)
}

// TestDoc checks that tt emmylua doc passes its flags to emmylua_doc_cli
// with the configuration, the arguments after -- and the paths. It runs
// programs, so it is not parallel, as TestCheck.
func TestDoc(t *testing.T) {
	project := t.TempDir()
	writeProgram(t, project, "emmylua_doc_cli", "echo \"$@\"\n")

	got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "doc", "-o", "doc/api", "--output-format", "markdown",
		"--", "--site-name", "App")
	require.NoError(t, got.Err)
	assert.Equal(t, "--config "+configPath(project)+
		" --output doc/api --output-format markdown --site-name App "+project+"\n", got.Stdout)
}

// TestFmt checks that tt emmylua fmt runs luafmt with its mode - --check
// unless --write or a flag after -- chooses another - the arguments after
// -- and the paths, without the configuration of the analyzer, and succeeds
// as the program does. It runs programs, so it is not parallel, as
// TestCheck.
func TestFmt(t *testing.T) {
	project := t.TempDir()
	writeProgram(t, project, "luafmt", "echo \"$@\"\n")

	for _, testCase := range []struct {
		args []string
		want string
	}{
		{args: nil, want: "--check " + project},
		{args: []string{"app"}, want: "--check app"},
		{args: []string{"app", "--check"}, want: "--check app"},
		{args: []string{"app", "--write"}, want: "--write app"},
		{args: []string{"app", "--", "--check"}, want: "--check app"},
		{args: []string{"--", "--list-different"}, want: "--list-different " + project},
		{args: []string{"--", "--init=luafmt.toml"}, want: "--init=luafmt.toml " + project},
		{args: []string{"--", "--tab"}, want: "--check --tab " + project},
	} {
		got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
			append([]string{"emmylua", "fmt"}, testCase.args...)...)
		require.NoError(t, got.Err, testCase.args)
		assert.Equal(t, testCase.want+"\n", got.Stdout, testCase.args)
	}

	assert.NoFileExists(t, configPath(project))

	got := sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "fmt", "--check", "--write")
	require.ErrorContains(t, got.Err, "none of the others can be")
}

// TestConfig checks that tt emmylua config writes the configuration, valid
// JSON with the stubs of the Tarantool of the project among its libraries,
// prints its path, and warns of a Tarantool without stubs and of none.
func TestConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stubs := filepath.Join(root, "share", "tarantool", "emmylua")
	require.NoError(t, os.MkdirAll(stubs, 0o755))

	project := t.TempDir()
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

	got = sdktest.New(t, sdktest.WithTarantool(old, sdk.TarantoolVersion{Major: 3})).Run(
		emmylua.New, "emmylua", "config")
	require.NoError(t, got.Err)
	assert.Equal(t, []string{old + " has no EmmyLua stubs (share/tarantool/emmylua, " +
		"Tarantool 3.9 and newer): checking without the types of the Tarantool API"},
		warnings(got.Records))

	got = sdktest.Run(t, emmylua.New, "emmylua", "config")
	require.NoError(t, got.Err)
	assert.Equal(t, []string{noTarantool}, warnings(got.Records))
}

// TestNotPrepared checks that a command whose program the rocks of the
// project do not have says to run tt emmylua prepare.
func TestNotPrepared(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"check", "doc", "fmt", "ls"} {
		got := sdktest.Run(t, emmylua.New, "emmylua", command)
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
