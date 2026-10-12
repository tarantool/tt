package cmd

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/install"
)

// TestBinaryDefaultScope checks that all commands accept an omitted scope.
func TestBinaryDefaultScope(t *testing.T) {
	cases := [][]string{
		{"install", "tarantool"},
		{"install", "tarantool-ee"},
		{"install", "tt"},
		{"install", "tcm"},
		{"uninstall", "tarantool"},
		{"uninstall", "tarantool-ee"},
		{"uninstall", "tt"},
		{"uninstall", "tcm"},
		{"list"},
		{"switch", "tarantool", "3.0.0"},
		{"search", "tarantool"},
		{"search", "tarantool-ee"},
		{"search", "tt"},
		{"search", "tcm"},
		{"install", "tarantool-sdk", "3.0.0"},
		{"install", "sdk", "3.0.0"},
		{"install", "SDK", "3.0.0"},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase, " "), func(t *testing.T) {
			root := NewBinaryCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(testCase)

			command, _, err := root.Find(testCase)
			require.NoError(t, err)

			called := false

			command.RunE = func(cmd *cobra.Command, args []string) error {
				called = true

				return nil
			}

			require.NoError(t, root.Execute())
			assert.True(t, called)

			scope, err := command.Flags().GetString("scope")
			require.NoError(t, err)
			assert.Equal(t, "project", scope)
			assert.False(t, command.Flags().Changed("scope"))
		})
	}
}

func TestBinaryScopeFlag(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		scope binary.Scope
		want  string
	}{
		{
			name:  "default",
			args:  []string{"install", "tarantool"},
			scope: binary.ScopeProject,
		},
		{
			name:  "binary parent",
			args:  []string{"--scope", "project", "install", "tarantool"},
			scope: binary.ScopeProject,
		},
		{
			name:  "operation parent",
			args:  []string{"install", "--scope", "user", "tarantool"},
			scope: binary.ScopeUser,
		},
		{
			name:  "program",
			args:  []string{"install", "tarantool", "--scope=system"},
			scope: binary.ScopeSystem,
		},
		{
			name: "unknown",
			args: []string{"install", "tarantool", "--scope=global"},
			want: `unknown scope "global"`,
		},
		{
			name: "empty",
			args: []string{"install", "tarantool", "--scope="},
			want: `unknown scope ""`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restoreProcessState(t)

			root := NewCmdRoot()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append([]string{"binary"}, testCase.args...))

			command, _, err := root.Find([]string{"binary", "install", "tarantool"})
			require.NoError(t, err)

			called := false

			command.RunE = func(cmd *cobra.Command, args []string) error {
				called = true

				scope, err := cmd.Flags().GetString("scope")
				require.NoError(t, err)
				assert.Equal(t, testCase.scope.String(), scope)

				return nil
			}

			err = root.Execute()
			if testCase.want != "" {
				require.ErrorContains(t, err, testCase.want)
				assert.False(t, called)
			} else {
				require.NoError(t, err)
				assert.True(t, called)
			}
		})
	}
}

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
		{name: "local repository", args: []string{"--local-repo", "/sources", "3.0.0"}},
		{
			name: "conflicting source",
			args: []string{"--from-build", "/build", "--local-repo", "/sources"},
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

func TestBinaryInstallLocalRepoRequiresPath(t *testing.T) {
	for _, program := range binarySupportedPrograms {
		t.Run(program, func(t *testing.T) {
			command, _, err := NewBinaryCmd().Find([]string{"install", program})
			require.NoError(t, err)
			require.ErrorContains(t, command.ParseFlags([]string{"--local-repo"}),
				"flag needs an argument: --local-repo")
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
		[]string{"--force", "--local-repo", "/source files", "--no-clean", "--reinstall"}))

	for _, name := range []string{"force", "no-clean", "reinstall"} {
		value, err := install.Flags().GetBool(name)
		require.NoError(t, err)
		assert.True(t, value, name)
	}

	localRepo, err := install.Flags().GetString("local-repo")
	require.NoError(t, err)
	assert.Equal(t, "/source files", localRepo)

	search, _, err := root.Find([]string{"search", "tarantool"})
	require.NoError(t, err)
	require.NoError(t, search.ParseFlags([]string{"--local-repo", "/source files"}))

	local, err := search.Flags().GetString("local-repo")
	require.NoError(t, err)
	assert.Equal(t, "/source files", local)
}

func TestBinaryCommandOptionsAreIndependent(t *testing.T) {
	cases := []struct {
		command []string
		flag    string
		first   string
		second  string
	}{
		{[]string{"install", "tarantool"}, "scope", "project", "system"},
		{[]string{"install", "tarantool"}, "dynamic", "true", "false"},
		{[]string{"install", "tarantool"}, "from-build", "/first", "/second"},
		{[]string{"install", "tarantool"}, "local-repo", "/first", "/second"},
		{[]string{"install", "tarantool-sdk"}, "destination", "/first", "/second"},
		{[]string{"search", "tarantool-ee"}, "version", "3.0", "3.1"},
		{[]string{"search", "tarantool"}, "local-repo", "/first", "/second"},
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

func TestBinaryInstallAdapterOptions(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramEe, binary.ProgramTcm} {
		t.Run(program.String(), func(t *testing.T) {
			restoreProcessState(t)

			base := binary.BinaryContext{Scope: binary.ScopeUser, Verbose: true}
			options := install.InstallCtx{
				Force: true, KeepTemp: true, LocalRepo: "/bundles", Reinstall: true, DevBuild: true,
			}
			command := newInstallTarantoolEeCmd(&base, &options)

			if program == binary.ProgramTcm {
				command = newInstallTcmCmd(&base, &options)
			}

			// Flag registration initializes DevBuild to its default.
			require.NoError(t, command.ParseFlags([]string{"--dev"}))

			called := false
			run := binaryRunE(&base, &options, func(gotBase binary.BinaryContext,
				gotOptions install.InstallCtx, ctx *cmdcontext.CmdCtx, args []string,
			) error {
				called = true

				assert.Equal(t, base.Scope, gotBase.Scope)
				assert.Equal(t, options, gotOptions)
				assert.Same(t, &cmdCtx, ctx)
				assert.Equal(t, program.String(), ctx.CommandName)
				assert.Equal(t, []string{"3.4.0"}, args)

				return nil
			})
			require.NoError(t, run(command, []string{"3.4.0"}))
			assert.True(t, called)
		})
	}
}

// TestBinaryScopedCommandsWithoutConfig exercises the adapters without the legacy boot layer.
func TestBinaryScopedCommandsWithoutConfig(t *testing.T) {
	for _, scope := range []binary.Scope{binary.ScopeProject, binary.ScopeUser} {
		t.Run(scope.String(), func(t *testing.T) {
			restoreProcessState(t)

			previousOpts := cliOpts

			t.Cleanup(func() { cliOpts = previousOpts })

			cliOpts = nil

			work := t.TempDir()
			t.Chdir(work)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())

			cmdCtx.Cli.ConfigPath = filepath.Join(work, "tt.yaml")
			require.NoError(t, os.WriteFile(cmdCtx.Cli.ConfigPath, []byte("[broken"), 0o600))

			root, err := scope.ArtifactPath()
			require.NoError(t, err)

			installed := filepath.Join(root, "versions", "tcm", "tcm_1.3.0", "tcm")
			require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
			require.NoError(t, os.WriteFile(installed, []byte("binary"), 0o755))

			repo := filepath.Join(work, "bundles")
			require.NoError(t, os.Mkdir(repo, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(repo,
				"tcm-1.3.0.linux.amd64.tar.gz"), nil, 0o600))

			commands := [][]string{
				{"search", "tcm", "--local-repo", "bundles"},
			}
			for _, args := range commands {
				command := NewBinaryCmd()
				command.SetOut(io.Discard)
				command.SetErr(io.Discard)
				command.SetArgs(append([]string{"--scope", scope.String()}, args...))
				require.NoError(t, command.Execute(), args)
			}
		})
	}
}

func TestBinarySearchRequiresLocalRepositoryArgument(t *testing.T) {
	for _, program := range binarySupportedPrograms {
		t.Run(program, func(t *testing.T) {
			command, _, err := NewBinaryCmd().Find([]string{"search", program})
			require.NoError(t, err)
			require.ErrorContains(t, command.ParseFlags([]string{"--local-repo"}),
				"flag needs an argument: --local-repo")
		})
	}
}
