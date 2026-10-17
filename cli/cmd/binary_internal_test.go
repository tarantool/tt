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
	"github.com/tarantool/tt/v3/internal/binary/list"
	switcher "github.com/tarantool/tt/v3/internal/binary/switch"
	"github.com/tarantool/tt/v3/internal/binary/uninstall"
)

// TestBinaryDefaultScope checks that all commands accept an omitted scope.
func TestBinaryDefaultScope(t *testing.T) {
	cases := [][]string{
		{"install", "tarantool"},
		{"install", "tarantool-ee"},
		{"install", "tt"},
		{"install", "tt-ee"},
		{"install", "tcm"},
		{"uninstall", "tarantool"},
		{"uninstall", "tarantool-ee"},
		{"uninstall", "tt"},
		{"uninstall", "tt-ee"},
		{"uninstall", "tcm"},
		{"list"},
		{"switch", "tarantool", "3.0.0"},
		{"search", "tarantool"},
		{"search", "tarantool-ee"},
		{"search", "tarantool-sdk"},
		{"search", "sdk"},
		{"search", "SDK"},
		{"search", "tt"},
		{"search", "tt-ee"},
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

// TestBinaryInstallSDK uses the archive installer without requiring tt.yaml.
func TestBinaryInstallSDK(t *testing.T) {
	restoreProcessState(t)
	t.Chdir(t.TempDir())

	root := NewCmdRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	cmdCtx.Cli.ConfigPath = ""

	directory := filepath.Join(t.TempDir(), "missing")
	root.SetArgs([]string{
		"binary", "install", "sdk", "--local-dir", directory, "--dev",
	})

	command, err := root.ExecuteC()
	require.ErrorContains(t, err, "not matched with")
	assert.Equal(t, "tt binary install tarantool-sdk", command.CommandPath())
	assert.Equal(t, "tarantool-sdk", cmdCtx.CommandName)

	source, err := command.Flags().GetString("local-dir")
	require.NoError(t, err)
	assert.Equal(t, directory, source)

	development, err := command.Flags().GetBool("dev")
	require.NoError(t, err)
	assert.True(t, development)
	assert.NoDirExists(t, ".tarantool")
	require.ErrorContains(t, command.ParseFlags([]string{"--destination", directory}),
		"unknown flag: --destination")
}

// TestBinaryArgumentValidation checks the argument contracts on the new paths
// without making network requests or modifying an installed program.
func TestBinaryArgumentValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install", "tarantool-sdk", "3.0", "3.1"}, "accepts at most 1 arg(s)"},
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
		{
			name: "local repository",
			args: []string{"--local-repo", "/sources", "3.0.0"},
		},
		{
			name: "conflicting source build",
			args: []string{"--from-build", "/build", "--source-build"},
			want: "group [from-build source-build]",
		},
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

func TestBinaryLocalSourceFlags(t *testing.T) {
	for _, operation := range []string{"install", "search"} {
		for _, testCase := range []struct {
			program string
			flag    string
			reject  string
		}{
			{"tt", "local-repo", "local-dir"},
			{"tarantool", "local-repo", "local-dir"},
			{"tarantool-ee", "local-dir", "local-repo"},
			{"tt-ee", "local-dir", "local-repo"},
			{"tarantool-sdk", "local-dir", "local-repo"},
			{"tcm", "local-dir", "local-repo"},
		} {
			t.Run(operation+"/"+testCase.program, func(t *testing.T) {
				command, _, err := NewBinaryCmd().Find([]string{operation, testCase.program})
				require.NoError(t, err)
				require.ErrorContains(t, command.ParseFlags([]string{"--" + testCase.flag}),
					"flag needs an argument: --"+testCase.flag)
				require.NoError(t, command.ParseFlags(
					[]string{"--" + testCase.flag, "/local files"}))

				directory, err := command.Flags().GetString(testCase.flag)
				require.NoError(t, err)
				assert.Equal(t, "/local files", directory)

				if operation == "install" &&
					(testCase.program == "tarantool" || testCase.program == "tt") {
					require.NoError(t, command.ParseFlags([]string{"--local-dir", "/bundles"}))
				} else {
					require.ErrorContains(t, command.ParseFlags(
						[]string{"--" + testCase.reject, "/local files"}),
						"unknown flag: --"+testCase.reject)
				}
			})
		}
	}
}

func TestBinaryInstallEditionFlags(t *testing.T) {
	for _, testCase := range []struct {
		program string
		args    []string
		want    string
	}{
		{"tarantool", []string{"3.8ee", "--local-dir", "/bundles"}, ""},
		{"tarantool", []string{"gc64-3.8.0-0-r94", "--dev"}, ""},
		{"tarantool", []string{"3.8ce", "--local-repo", "/sources"}, ""},
		{"tarantool", []string{"3.8ee", "--local-repo", "/sources"},
			"--local-repo is not supported"},
		{"tarantool", []string{"3.8ee", "--dynamic"}, "--dynamic is not supported"},
		{"tarantool", []string{"3.8ee", "--source-build"}, "--source-build is not supported"},
		{"tarantool", []string{"3.8ee", "--source-build=false"},
			"--source-build is not supported"},
		{"tarantool", []string{"3.8ee", "--use-docker"}, "--use-docker is not supported"},
		{"tarantool", []string{"3.8", "--local-dir", "/bundles"}, "--local-dir is not supported"},
		{"tarantool", []string{"3.8ce", "--dev"}, "--dev is not supported"},
		{"tt", []string{"2.15ee", "--local-dir", "/bundles"}, ""},
		{"tt", []string{"2.15ce", "--local-repo", "/sources"}, ""},
		{"tt", []string{"abcdefce", "--local-repo", "/sources"}, ""},
		{"tt", []string{"pr/123", "--local-repo", "/sources"}, ""},
		{"tt", []string{"2.15ee", "--local-repo", "/sources"}, "--local-repo is not supported"},
		{"tt", []string{"2.15", "--local-dir", "/bundles"}, "--local-dir is not supported"},
	} {
		t.Run(testCase.program+"/"+strings.Join(testCase.args, " "), func(t *testing.T) {
			command, _, err := NewBinaryCmd().Find([]string{"install", testCase.program})
			require.NoError(t, err)
			require.NoError(t, command.ParseFlags(testCase.args))

			err = command.ValidateArgs(command.Flags().Args())
			if testCase.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.want)
			}
		})
	}
}

func TestBinaryUnifiedCompletion(t *testing.T) {
	t.Chdir(t.TempDir())

	root := filepath.Join(".tarantool", "versions")
	for _, program := range []string{"tarantool", "tarantool-ee", "tt", "tt-ee"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, program, program+"_3.8.0"), 0o755))
	}

	for _, program := range []string{"tarantool", "tt"} {
		command, _, err := NewBinaryCmd().Find([]string{"uninstall", program})
		require.NoError(t, err)
		require.NoError(t, command.ParseFlags(nil))

		versions, _ := command.ValidArgsFunction(command, nil, "")
		assert.Equal(t, []string{"3.8.0", "3.8.0ee"}, versions)
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
			root.SetArgs(append(testCase.args, "--local-dir", destination))

			err := root.Execute()
			require.ErrorContains(t, err, "not matched with")
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
		{[]string{"install", "tarantool"}, "source-build", "true", "false"},
		{[]string{"install", "tarantool"}, "from-build", "/first", "/second"},
		{[]string{"install", "tarantool"}, "local-repo", "/first", "/second"},
		{[]string{"install", "tarantool-ee"}, "local-dir", "/first", "/second"},
		{[]string{"install", "tarantool-sdk"}, "local-dir", "/first", "/second"},
		{[]string{"search", "tarantool-ee"}, "version", "3.0", "3.1"},
		{[]string{"search", "tarantool"}, "local-repo", "/first", "/second"},
		{[]string{"search", "tcm"}, "local-dir", "/first", "/second"},
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
			command := newInstallEnterpriseCmd(&base, &options, binary.ProgramEe)

			if program == binary.ProgramTcm {
				command = newInstallStandaloneCmd(&base, &options, program)
			}

			// Flag registration initializes options to their defaults.
			require.NoError(t, command.ParseFlags([]string{"--dev", "--local-dir", "/bundles"}))

			called := false
			run := binaryRunE(&base, &options, func(gotBase binary.BinaryContext,
				gotOptions install.InstallCtx, ctx *cmdcontext.CmdCtx, args []string,
			) error {
				called = true

				assert.Equal(t, base.Scope, gotBase.Scope)
				assert.Equal(t, options, gotOptions)
				assert.Equal(t, "/bundles", gotOptions.LocalRepo)
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
				{"switch", "tcm", "1.3.0"},
				{"list"},
				{"search", "tcm", "--local-dir", "bundles"},
			}
			for _, args := range commands {
				command := NewBinaryCmd()
				command.SetOut(io.Discard)
				command.SetErr(io.Discard)
				command.SetArgs(append([]string{"--scope", scope.String()}, args...))
				require.NoError(t, command.Execute(), args)
			}

			active, err := filepath.EvalSymlinks(filepath.Join(root, "bin", "tcm"))
			require.NoError(t, err)
			assert.Equal(t, installed, active)

			command := NewBinaryCmd()
			command.SetArgs([]string{"--scope", scope.String(), "uninstall", "tcm", "1.3.0"})
			require.NoError(t, command.Execute())
			assert.NoDirExists(t, filepath.Dir(installed))

			_, err = os.Lstat(filepath.Join(root, "bin", "tcm"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestBinarySwitchPreservesGitRefs(t *testing.T) {
	for _, program := range []string{"tt", "tarantool", "tarantool-ee", "tcm"} {
		for _, ref := range []string{"master", "123abcd", "1234567"} {
			t.Run(program+"/"+ref, func(t *testing.T) {
				restoreProcessState(t)
				t.Chdir(t.TempDir())

				parsed, err := binary.ParseProgram(program)
				require.NoError(t, err)

				root, err := binary.ScopeProject.ArtifactPath()
				require.NoError(t, err)

				installation := filepath.Join(root, "versions", program, program+"_"+ref)
				installed := filepath.Join(installation, parsed.Exec())
				require.NoError(t, os.MkdirAll(installation, 0o755))
				require.NoError(t, os.WriteFile(installed, nil, 0o755))

				if parsed.IsTarantool() {
					require.NoError(t, os.MkdirAll(filepath.Join(installation, "include",
						parsed.Exec()), 0o755))
				}

				command := NewBinaryCmd()
				command.SetOut(io.Discard)
				command.SetErr(io.Discard)
				command.SetArgs([]string{"switch", program, ref})
				require.NoError(t, command.Execute())

				active, err := filepath.EvalSymlinks(filepath.Join(root, "bin", parsed.Exec()))
				require.NoError(t, err)
				assert.Equal(t, installed, active)

				if parsed.IsTarantool() {
					headers, err := filepath.EvalSymlinks(
						filepath.Join(root, "include", parsed.Exec()))
					require.NoError(t, err)
					assert.Equal(t, filepath.Join(installation, "include", parsed.Exec()), headers)
				}
			})
		}
	}
}

func TestBinaryScopedAdaptersDoNotFindParent(t *testing.T) {
	restoreProcessState(t)

	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0o755))

	installed := filepath.Join(parent, ".tarantool", "versions", "tt", "tt_v3.0.0", "tt")
	require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
	require.NoError(t, os.WriteFile(installed, nil, 0o755))
	t.Chdir(child)

	base := binary.BinaryContext{Scope: binary.ScopeProject}
	ctx := &cmdcontext.CmdCtx{}
	err := internalListModule(base, list.ListCtx{}, ctx, nil)
	require.ErrorIs(t, err, list.ErrNoBinariesInstalled)
	require.ErrorContains(t, internalSwitchModule(base, switcher.SwitchCtx{}, ctx,
		[]string{"tt", "3.0.0"}), "is not installed")

	ctx.CommandName = "tt"
	require.ErrorContains(t, internalUninstallModule(base, uninstall.UninstallCtx{}, ctx,
		[]string{"3.0.0"}), "not installed")
	assert.FileExists(t, installed)
	assert.NoDirExists(t, filepath.Join(child, ".tarantool"))
	assert.NoDirExists(t, filepath.Join(parent, ".tarantool", "bin"))
}

func TestBinaryCredentialsFile(t *testing.T) {
	cases := [][]string{
		{"search", "tarantool-ee"},
		{"search", "tarantool-sdk"},
		{"search", "sdk"},
		{"search", "SDK"},
		{"search", "tcm"},
		{"install", "tarantool-ee", "gc64-3.4.0-0-r60"},
		{"install", "tarantool", "gc64-3.4.0-0-r60"},
		{"install", "tarantool", "3.4ee"},
		{"install", "tcm", "1.3.1-0-g074b5ffa"},
		{"install", "tarantool-sdk", "gc64-3.4.0-0-r60"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			restoreProcessState(t)
			t.Chdir(t.TempDir())
			t.Setenv("TT_CLI_EE_USERNAME", "")
			t.Setenv("TT_CLI_EE_PASSWORD", "")

			// If the flag is lost, the operation prompts instead of reporting this file.
			credentials := "missing-credentials"
			command := NewBinaryCmd()
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(append(args, "--credentials-file", credentials))
			require.ErrorContains(t, command.Execute(),
				"opening the credentials file: open "+credentials)
		})
	}
}

func TestBinaryUninstallCompletionScope(t *testing.T) {
	restoreProcessState(t)

	previousOpts := cliOpts

	t.Cleanup(func() { cliOpts = previousOpts })

	cliOpts = nil

	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	t.Chdir(child)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	projectRoot, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	userRoot, err := binary.ScopeUser.ArtifactPath()
	require.NoError(t, err)

	for _, program := range binarySupportedPrograms {
		parsed, err := binary.ParseProgram(program)
		require.NoError(t, err)

		for root, version := range map[string]string{
			projectRoot: "1.0.0", userRoot: "2.0.0", filepath.Join(parent, ".tarantool"): "3.0.0",
		} {
			installed := filepath.Join(root, "versions", program,
				program+"_"+version, parsed.Exec())
			require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
			require.NoError(t, os.WriteFile(installed, nil, 0o755))
		}

		for _, testCase := range []struct {
			scope    string
			expected []string
		}{
			{expected: []string{"1.0.0"}},
			{scope: "project", expected: []string{"1.0.0"}},
			{scope: "user", expected: []string{"2.0.0"}},
			{scope: "global"},
		} {
			command, _, err := NewBinaryCmd().Find([]string{"uninstall", program})
			require.NoError(t, err)

			var flags []string

			if testCase.scope != "" {
				flags = []string{"--scope", testCase.scope}
			}

			require.NoError(t, command.ParseFlags(flags))

			// Completion must work before PersistentPreRunE populates the shared context.
			versions, directive := command.ValidArgsFunction(command, nil, "")
			assert.Equal(t, testCase.expected, versions)
			assert.NotZero(t, directive&cobra.ShellCompDirectiveNoFileComp)
			assert.Equal(t, testCase.scope == "global",
				directive&cobra.ShellCompDirectiveError != 0)

			versions, directive = command.ValidArgsFunction(command, []string{"1.0.0"}, "")
			assert.Empty(t, versions)
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		}
	}
}
