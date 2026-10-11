package cmd

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBinaryInstallSDK checks that SDK retains the download flags and reaches
// the downloader without requiring an environment config or installing a binary.
//
//nolint:paralleltest // Command handlers use process state.
func TestBinaryInstallSDK(t *testing.T) {
	restoreProcessState(t)

	root := NewCmdRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	cmdCtx.Cli.ConfigPath = ""

	destination := filepath.Join(t.TempDir(), "missing")
	root.SetArgs([]string{
		"binary", "install", "--destination", destination, "tarantool-sdk", "3.0.0", "--dev",
	})

	command, err := root.ExecuteC()
	require.ErrorContains(t, err, "invalid destination directory")
	require.ErrorContains(t, err, destination)
	assert.Equal(t, "tt binary install tarantool-sdk", command.CommandPath())
	assert.Equal(t, "tarantool-sdk", cmdCtx.CommandName)

	directory, err := command.Flags().GetString("destination")
	require.NoError(t, err)
	assert.Equal(t, destination, directory)

	devBuild, err := command.Flags().GetBool("dev")
	require.NoError(t, err)
	assert.True(t, devBuild)
}

// TestBinaryArgumentValidation checks the argument contracts on the new paths
// without making network requests or modifying an installed program.
func TestBinaryArgumentValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install", "tarantool-sdk"}, "you need to specify the version"},
		{[]string{"install", "tarantool-sdk", "3.0", "3.1"}, "invalid number of parameters"},
		{[]string{"install", "tarantool", "3.0", "3.1"}, "accepts at most 1 arg(s)"},
		{[]string{"uninstall", "tarantool", "3.0", "3.1"}, "accepts at most 1 arg(s)"},
		{[]string{"search", "tarantool", "3.0"}, "accepts 0 arg(s)"},
		{[]string{"switch", "unknown"}, "not supported program: unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			command, args, err := NewBinaryCmd().Find(tc.args)
			require.NoError(t, err)
			require.NotNil(t, command.Args)
			assert.ErrorContains(t, command.ValidateArgs(args), tc.want)
		})
	}
}

func TestBinaryInstallFromBuildOptions(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "build", args: []string{"--from-build", "/build"}},
		{name: "headers", args: []string{"--from-build", "/build", "--include-dir", "/headers"}},
		{name: "local repository", args: []string{"--local-repo", "3.0.0"}},
		{
			name: "conflicting source", args: []string{"--from-build", "/build", "--local-repo"},
			want: "group [from-build local-repo]",
		},
		{
			name: "conflicting docker", args: []string{"--from-build", "/build", "--use-docker"},
			want: "group [from-build use-docker]",
		},
		{
			name: "conflicting linking", args: []string{"--from-build", "/build", "--dynamic"},
			want: "group [from-build dynamic]",
		},
		{
			name: "extra argument", args: []string{"--from-build", "/build", "unexpected"},
			want: "accepts 0 arg(s), received 1",
		},
		{
			name: "empty directory", args: []string{"--from-build="},
			want: "--from-build requires a non-empty directory",
		},
		{
			name: "headers without build", args: []string{"--include-dir", "/headers"},
			want: "--include-dir requires --from-build",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			command, _, err := NewBinaryCmd().Find([]string{"install", "tarantool"})
			require.NoError(t, err)
			require.NoError(t, command.ParseFlags(testCase.args))

			err = command.ValidateArgs(command.Flags().Args())
			if err == nil {
				err = command.ValidateFlagGroups()
			}

			if testCase.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.want)
			}
		})
	}
}

func TestBinaryInheritedOptions(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		verbose bool
	}{
		{
			name:    "parent",
			args:    []string{"binary", "--verbose", "install", "sdk", "3.0"},
			verbose: true,
		},
		{
			name:    "leaf",
			args:    []string{"binary", "install", "sdk", "3.0", "--verbose"},
			verbose: true,
		},
		{
			name:    "legacy",
			args:    []string{"--verbose", "binary", "install", "SDK", "3.0"},
			verbose: true,
		},
		{
			name: "override",
			args: []string{"--verbose", "binary", "install", "sdk", "3.0",
				"--verbose=false"},
			verbose: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restoreProcessState(t)

			root := NewCmdRoot()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)

			destination := filepath.Join(t.TempDir(), "missing")
			root.SetArgs(append(testCase.args, "--destination", destination))

			err := root.Execute()
			require.ErrorContains(t, err, "invalid destination directory")
			assert.Equal(t, testCase.verbose, cmdCtx.Cli.Verbose)
			assert.Equal(t, testCase.verbose, slog.Default().Enabled(t.Context(), slog.LevelDebug))
		})
	}
}

func TestBinaryParentFlagsAfterProgram(t *testing.T) {
	restoreProcessState(t)

	root := NewBinaryCmd()

	install, _, err := root.Find([]string{"install", "tarantool"})
	require.NoError(t, err)
	require.NoError(t, install.ParseFlags(
		[]string{"--force", "--local-repo", "--no-clean", "--reinstall"}))

	for _, name := range []string{"force", "local-repo", "no-clean", "reinstall"} {
		value, err := install.Flags().GetBool(name)
		require.NoError(t, err)
		assert.True(t, value, name)
	}

	search, _, err := root.Find([]string{"search", "tarantool"})
	require.NoError(t, err)
	require.NoError(t, search.ParseFlags([]string{"--local-repo"}))

	local, err := search.Flags().GetBool("local-repo")
	require.NoError(t, err)
	assert.True(t, local)
}

func TestBinaryCommandOptionsAreIndependent(t *testing.T) {
	cases := []struct {
		command []string
		flag    string
		first   string
		second  string
	}{
		{[]string{"install", "tarantool"}, "dynamic", "true", "false"},
		{[]string{"install", "tarantool"}, "from-build", "/first", "/second"},
		{[]string{"install", "tarantool-sdk"}, "destination", "/first", "/second"},
		{[]string{"search", "tarantool-ee"}, "version", "3.0", "3.1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.flag, func(t *testing.T) {
			first, _, err := NewBinaryCmd().Find(testCase.command)
			require.NoError(t, err)
			require.NoError(t, first.ParseFlags(
				[]string{"--" + testCase.flag + "=" + testCase.first}))

			second, _, err := NewBinaryCmd().Find(testCase.command)
			require.NoError(t, err)
			assert.Equal(t, testCase.first, first.Flags().Lookup(testCase.flag).Value.String())
			require.NoError(t, second.ParseFlags(
				[]string{"--" + testCase.flag + "=" + testCase.second}))
			assert.Equal(t, testCase.first, first.Flags().Lookup(testCase.flag).Value.String())
			assert.Equal(t, testCase.second, second.Flags().Lookup(testCase.flag).Value.String())
		})
	}
}
