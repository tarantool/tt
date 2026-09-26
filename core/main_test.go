package core_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/printing"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/core"
)

// mainCaseEnv selects, in a process the tests start, the program TestMain
// runs instead of the tests: a key of mainCases.
const mainCaseEnv = "TT_CORE_TEST_MAIN"

// brokenModule is a module whose constructor panics.
func brokenModule(sdk.Services) []sdk.Mount {
	panic("constructor bug")
}

// mainCases are the programs the tests run tt as, each in its own process:
// Main is once per process.
var mainCases = map[string]func() int{
	"builtin": func() int {
		return core.Main(core.Modules{"builtin": core.Builtin})
	},
	"demo": func() int {
		return core.Main(core.Modules{"builtin": core.Builtin, "demo": demoModule})
	},
	"conflict": func() int {
		return core.Main(core.Modules{
			"builtin": core.Builtin,
			"dup": func(sdk.Services) []sdk.Mount {
				return []sdk.Mount{{Path: "", Cmd: &cobra.Command{Use: "version", Run: nothing}}}
			},
		})
	},
	"early": func() int {
		return core.Main(core.Modules{
			"builtin": core.Builtin,
			"early": func(services sdk.Services) []sdk.Mount {
				_, _ = services.Tarantool()

				return nil
			},
		})
	},
	"panic": func() int {
		return core.Main(core.Modules{"builtin": core.Builtin, "broken": brokenModule})
	},
	"flavour": func() int {
		return core.Main(core.Modules{"builtin": core.Builtin}, core.WithFlavour(eeFlavour))
	},
	"flavour-panic": func() int {
		return core.Main(core.Modules{"builtin": core.Builtin, "broken": brokenModule},
			core.WithFlavour(eeFlavour))
	},
	"ee-main": func() int {
		cmd.InjectedCmds = append(cmd.InjectedCmds, newEEVersionCmd())

		return core.Main(core.Modules{"builtin": core.Builtin})
	},
	"injected": func() int {
		cmd.InjectedCmds = append(cmd.InjectedCmds, newInjectedCmd())

		return core.Main(core.Modules{"builtin": core.Builtin, "demo": demoModule})
	},
	"no-extmod": func() int {
		return core.Main(core.Modules{"builtin": core.Builtin, "demo": demoModule},
			core.WithoutExternalModules())
	},
}

// eeFlavour is a distribution with a name, a version and an edition of its
// own.
var eeFlavour = core.Flavour{
	Title: "Tarantool CLI EE",
	Version: core.VersionInfo{
		Tag: "v2.15.0-3-gdef5678", Commit: "def5678", CommitsSinceTag: 3, Label: "",
	},
	Edition: "ee",
}

// TestMain runs the program mainCaseEnv selects, when it does, and the tests
// otherwise.
func TestMain(m *testing.M) {
	if name, ok := os.LookupEnv(mainCaseEnv); ok {
		program, known := mainCases[name]
		if !known {
			_, _ = fmt.Fprintf(os.Stderr, "unknown %s=%q\n", mainCaseEnv, name)

			os.Exit(100) // Anything a case cannot exit with.
		}

		os.Exit(program())
	}

	os.Exit(m.Run())
}

// nothing is a Run that does nothing.
func nothing(*cobra.Command, []string) {}

// errPlain is a failure with no exit code of its own.
var errPlain = errors.New("plain failure")

// demoResult is what "demo print" emits.
type demoResult struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Human renders the result for people.
func (r demoResult) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s: %d\n", r.Name, r.Count)

	return err
}

// demoModule is a module using every service, with commands failing in
// every way that decides an exit code.
func demoModule(services sdk.Services) []sdk.Mount {
	fail := func(err error) func(*cobra.Command, []string) error {
		return func(*cobra.Command, []string) error { return err }
	}
	out := func() io.Writer { return services.Streams().IO().Out }

	demo := &cobra.Command{Use: "demo", Short: "Demonstrate the services"}

	var format *output.FormatFlag

	printCmd := &cobra.Command{
		Use: "print",
		RunE: func(*cobra.Command, []string) error {
			printer, err := services.Streams().Printer(format.Format())
			if err != nil {
				return err
			}

			err = printer.Emit(demoResult{Name: "demo", Count: 2})

			return err
		},
	}

	format = output.BindFormat(printCmd.Flags(), output.FormatHuman, printing.Formats()...)

	demo.AddCommand(
		&cobra.Command{Use: "ok", RunE: func(*cobra.Command, []string) error {
			services.Log().Info("hello from demo")

			_, err := fmt.Fprintln(out(), "ok")

			return err
		}},
		&cobra.Command{Use: "code3", RunE: fail(sdk.WithCode(sdk.ExitPartial,
			errors.New("one of two failed")))},
		&cobra.Command{Use: "net", RunE: fail(fmt.Errorf("fetching: %w", &net.OpError{
			Op: "dial", Net: "tcp", Err: errors.New("connection refused"),
		}))},
		&cobra.Command{Use: "plain", RunE: fail(errPlain)},
		&cobra.Command{Use: "usage", RunE: fail(sdk.WithUsage(errors.New("bad argument")))},
		&cobra.Command{Use: "project", RunE: func(*cobra.Command, []string) error {
			dir, err := services.Project().Dir()
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(out(), dir)

			return err
		}},
		&cobra.Command{Use: "confirm", RunE: func(*cobra.Command, []string) error {
			answer, err := services.Confirm("Proceed?", true)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(out(), "answer=%v\n", answer)

			return err
		}},
		printCmd,
		newDemoClusterCmd(services),
		&cobra.Command{
			Use:  "cluster-dir",
			Args: cobra.ExactArgs(1),
			RunE: func(command *cobra.Command, args []string) error {
				cfg, err := services.ClusterConfig(command.Context(),
					sdk.ParseClusterSource(args[0], sdk.Credentials{}))
				if err != nil {
					return err
				}

				_, err = fmt.Fprintln(services.Streams().IO().Out, cfg.Dir)

				return err
			},
		},
		&cobra.Command{Use: "exit", RunE: func(*cobra.Command, []string) error {
			services.Exit(sdk.WithCode(sdk.ExitPartial, errors.New("exited early")))

			return errors.New("Exit returned")
		}},
		&cobra.Command{Use: "exit-net", RunE: func(*cobra.Command, []string) error {
			services.Exit(fmt.Errorf("fetching: %w", &net.OpError{
				Op: "dial", Net: "tcp", Err: errors.New("connection refused"),
			}))

			return errors.New("Exit returned")
		}},
	)

	return []sdk.Mount{{Path: "", Cmd: demo}}
}

// newDemoClusterCmd returns "demo cluster <source> [<instance>]": it prints
// the instances of the cluster configuration of the source, then the
// database mode of the instance when one is named. A source or an instance
// that is not found is printed as "not found: <error>" and is no failure.
func newDemoClusterCmd(services sdk.Services) *cobra.Command {
	return &cobra.Command{
		Use:  "cluster",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, args []string) error {
			out := services.Streams().IO().Out

			cfg, err := services.ClusterConfig(command.Context(),
				sdk.ParseClusterSource(args[0], sdk.Credentials{}))
			if errors.Is(err, sdk.ErrNotFound) {
				_, err = fmt.Fprintf(out, "not found: %v\n", err)

				return err
			}

			if err != nil {
				return err
			}

			names, err := sdk.Instances(cfg.Config)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(out, strings.Join(names, " "))
			if err != nil || len(args) == 1 {
				return err
			}

			instance, err := sdk.InstanceConfig(cfg.Config, args[1])
			if errors.Is(err, sdk.ErrNotFound) {
				_, err = fmt.Fprintf(out, "not found: %v\n", err)

				return err
			}

			if err != nil {
				return err
			}

			var mode string

			_, err = instance.Get(goconfig.NewKeyPath("database/mode"), &mode)
			if err != nil {
				return fmt.Errorf("database mode: %w", err)
			}

			_, err = fmt.Fprintf(out, "mode=%s\n", mode)

			return err
		},
	}
}

// newEEVersionCmd returns a command shaped like tt-ee's version: injected,
// reading the process's CmdCtx when it runs.
func newEEVersionCmd() *cobra.Command {
	ctx := cmd.GetCmdCtxPtr()

	return &cobra.Command{
		Use:   "version",
		Short: "Show the EE version",
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(os.Stdout, "EE version, command %s, verbose=%v\n",
				command.Name(), ctx.Cli.Verbose)

			return err
		},
	}
}

// newInjectedCmd returns a command injected through cmd.InjectedCmds that
// prints its arguments.
func newInjectedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "injected",
		Short: "Print the arguments",
		RunE: func(_ *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(os.Stdout, strings.Join(append([]string{"internal"}, args...),
				" "))

			return err
		},
	}
}

// result is how a tt process ended.
type result struct {
	code   int
	stdout string
	stderr string
}

// ttRun describes one tt process.
type ttRun struct {
	program string
	args    []string
	env     []string
	stdin   string
	dir     string
}

// runTT runs the test binary as the program of spec and returns how it
// ended. The process starts in a directory of its own, with no tt
// configuration and no external modules unless spec's environment sets them.
func runTT(t *testing.T, spec ttRun) result {
	t.Helper()

	//nolint:gosec // The test binary itself.
	process := exec.CommandContext(t.Context(), os.Args[0], spec.args...)

	process.Dir = spec.dir
	if process.Dir == "" {
		process.Dir = t.TempDir()
	}

	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TT_") {
			continue
		}

		process.Env = append(process.Env, entry)
	}

	process.Env = append(process.Env, mainCaseEnv+"="+spec.program)
	process.Env = append(process.Env, spec.env...)
	process.Stdin = strings.NewReader(spec.stdin)

	var stdout, stderr bytes.Buffer

	process.Stdout = &stdout
	process.Stderr = &stderr

	err := process.Run()

	var exitErr *exec.ExitError

	if err != nil && !errors.As(err, &exitErr) {
		require.NoError(t, err)
	}

	return result{
		code: process.ProcessState.ExitCode(), stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

// TestMainExitCodes checks the exit code and the report of every way a
// command can end.
func TestMainExitCodes(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		program string
		args    []string
		code    int
		stdout  string
		stderr  []string
		usage   bool
	}{
		{name: "success", program: "demo", args: []string{"demo", "ok"}, code: 0, stdout: "ok\n"},
		{
			name: "module code 3", program: "demo", args: []string{"demo", "code3"}, code: 3,
			stderr: []string{"one of two failed"},
		},
		{
			name: "network failure", program: "demo", args: []string{"demo", "net"}, code: 2,
			stderr: []string{"connection refused"},
		},
		{
			name: "plain error", program: "demo", args: []string{"demo", "plain"}, code: 1,
			stderr: []string{"plain failure"},
		},
		{
			name: "usage error", program: "demo", args: []string{"demo", "usage"}, code: 1,
			stderr: []string{"bad argument"}, usage: true,
		},
		// cobra reports what it detects itself, with the usage.
		{
			name: "unknown command", program: "demo", args: []string{"nosuch"}, code: 1,
			stderr: []string{"unknown command nosuch"}, usage: true,
		},
		{
			name: "unknown flag", program: "demo", args: []string{"demo", "ok", "--nosuch"},
			code: 1, stderr: []string{"unknown flag: --nosuch"}, usage: true,
		},
		{
			name: "unknown root flag", program: "builtin", args: []string{"--nosuch"},
			code: 1, stderr: []string{"unknown flag: --nosuch"}, usage: true,
		},
		{
			name: "mount conflict", program: "conflict", args: []string{"version"}, code: 1,
			stderr: []string{`duplicate command "version": modules "builtin" and "dup"`},
		},
		{
			name: "services in a constructor", program: "early", args: []string{"version"},
			code: 1, stderr: []string{`module "early" used Services.Tarantool before tt ` +
				`was configured`},
		},
		{
			name: "constructor panic", program: "panic", args: []string{"version"}, code: 1,
			stderr: []string{`module "broken" panicked while building its commands: ` +
				`constructor bug`},
		},
		// Services.Exit ends tt as a returned error would.
		{
			name: "exit with code 3", program: "demo", args: []string{"demo", "exit"}, code: 3,
			stderr: []string{"exited early"},
		},
		{
			name: "exit with a system failure", program: "demo", args: []string{"demo", "exit-net"},
			code: 2, stderr: []string{"connection refused"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := runTT(t, ttRun{program: testCase.program, args: testCase.args})

			assert.Equal(t, testCase.code, got.code, "stderr: %s", got.stderr)
			assert.Equal(t, testCase.stdout, got.stdout)

			for _, want := range testCase.stderr {
				assert.Equal(t, 1, strings.Count(got.stderr, want),
					"reported once: %q in %s", want, got.stderr)
			}

			assert.Equal(t, testCase.usage, strings.Contains(got.stderr, "USAGE"), got.stderr)
		})
	}
}

// TestMainServices checks the services a module's command uses.
func TestMainServices(t *testing.T) {
	t.Parallel()

	t.Run("project", func(t *testing.T) {
		t.Parallel()

		dir, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)

		got := runTT(t, ttRun{program: "demo", args: []string{"demo", "project"}, dir: dir})
		require.Equal(t, 0, got.code, got.stderr)
		assert.Equal(t, dir+"\n", got.stdout)
	})

	t.Run("confirm", func(t *testing.T) {
		t.Parallel()

		got := runTT(t, ttRun{
			program: "demo", args: []string{"demo", "confirm"}, stdin: "maybe\nn\n",
		})
		require.Equal(t, 0, got.code, got.stderr)
		assert.Equal(t, "answer=false\n", got.stdout)
		assert.Equal(t, "Proceed? [y/n]: Proceed? [y/n]: ", got.stderr)
	})

	t.Run("confirm with --no-prompt", func(t *testing.T) {
		t.Parallel()

		got := runTT(t, ttRun{
			program: "demo", args: []string{"--no-prompt", "demo", "confirm"}, stdin: "n\n",
		})
		require.Equal(t, 0, got.code, got.stderr)
		assert.Equal(t, "answer=true\n", got.stdout)
		assert.Empty(t, got.stderr)
	})

	t.Run("yaml", func(t *testing.T) {
		t.Parallel()

		got := runTT(t, ttRun{program: "demo", args: []string{"demo", "print", "-o", "yaml"}})
		require.Equal(t, 0, got.code, got.stderr)
		assert.Equal(t, "name: demo\ncount: 2\n", got.stdout)
	})

	t.Run("log", func(t *testing.T) {
		t.Parallel()

		got := runTT(t, ttRun{
			program: "demo", args: []string{"--log-format", "json", "demo", "ok"},
		})
		require.Equal(t, 0, got.code, got.stderr)
		assert.Contains(t, got.stderr, `"msg":"hello from demo","module":"demo"`)
	})
}

// TestMainEEShape checks that a command injected the way tt-ee injects its
// version works through Main.
func TestMainEEShape(t *testing.T) {
	t.Parallel()

	got := runTT(t, ttRun{program: "ee-main", args: []string{"-V", "version"}})
	require.Equal(t, 0, got.code, got.stderr)
	assert.Equal(t, "EE version, command version, verbose=true\n", got.stdout)

	got = runTT(t, ttRun{program: "ee-main", args: []string{"version", "--help"}})
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "Show the EE version")
}

// TestMainWithFlavour checks that tt built with a flavour presents itself as
// the distribution: in tt version, in the help and in an internal error.
func TestMainWithFlavour(t *testing.T) {
	t.Parallel()

	platform := runtime.GOOS + "/" + runtime.GOARCH

	for _, testCase := range []struct {
		name   string
		args   []string
		stdout string
	}{
		{
			name: "full", args: []string{"version"},
			stdout: "Tarantool CLI EE version 2.15.0, " + platform +
				". commit: def5678 (v2.15.0-3-gdef5678)\n",
		},
		{name: "short", args: []string{"version", "--short"}, stdout: "2.15.0+ee\n"},
		{name: "commit", args: []string{"version", "--commit"}, stdout: "2.15.0+ee.def5678\n"},
		{
			name: "short with commit", args: []string{"version", "--short", "--commit"},
			stdout: "2.15.0+ee.def5678\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := runTT(t, ttRun{program: "flavour", args: testCase.args})
			require.Equal(t, 0, got.code, got.stderr)
			assert.Equal(t, testCase.stdout, got.stdout)
		})
	}

	t.Run("help", func(t *testing.T) {
		t.Parallel()

		got := runTT(t, ttRun{program: "flavour", args: []string{"--help"}})
		require.Equal(t, 0, got.code, got.stderr)
		assert.True(t, strings.HasPrefix(got.stdout,
			"Tarantool CLI EE — utility for managing Tarantool packages and "+
				"Tarantool-based\napplications\n"), got.stdout)

		got = runTT(t, ttRun{program: "flavour", args: []string{"version", "--help"}})
		require.Equal(t, 0, got.code, got.stderr)
		assert.True(t, strings.HasPrefix(got.stdout,
			"Show Tarantool CLI EE version information\n"), got.stdout)
	})

	t.Run("internal error", func(t *testing.T) {
		t.Parallel()

		got := runTT(t, ttRun{program: "flavour-panic", args: []string{"version"}})
		assert.Equal(t, 1, got.code, got.stderr)
		assert.Contains(t, got.stderr, "Version: Tarantool CLI EE version 2.15.0, "+platform)
	})
}

// writeExternalModules creates, in a modules directory of their own, the
// external modules names, each with an executable that prints its name and
// its arguments and exits 7. It returns the directory, for
// TT_CLI_MODULES_PATH.
func writeExternalModules(t *testing.T, names ...string) string {
	t.Helper()

	dir := t.TempDir()

	for _, name := range names {
		moduleDir := filepath.Join(dir, name)
		require.NoError(t, os.Mkdir(moduleDir, 0o755))

		script := "#!/bin/sh\necho \"external " + name + " $*\"\nexit 7\n"
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main"), []byte(script),
			0o755))

		manifest := "version: 1.0.0\nhelp: External " + name + "\nmain: main\n"
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "manifest.yaml"),
			[]byte(manifest), 0o644))
	}

	return dir
}

// warning returns the line tt logs a warning as.
func warning(message string) string {
	return "   ⚠ " + message + "\n"
}

// TestMainExternalModules checks what an external module named like a
// command does: it replaces the command, whoever built it and subcommands
// and all, without a warning, unless -I is given, and is ignored with a
// warning when the command is one tt keeps for itself.
func TestMainExternalModules(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		program string
		modules []string
		args    []string
		code    int
		stdout  string
		// prefix compares stdout with only the beginning of the output.
		prefix bool
		// stderr returns the whole stderr, given the modules directory.
		stderr func(dir string) string
	}{
		{
			name: "module command replaced", program: "demo", modules: []string{"demo"},
			args: []string{"demo", "ok", "--x"}, code: 7, stdout: "external demo ok --x\n",
		},
		{
			name: "module command kept with -I", program: "demo", modules: []string{"demo"},
			args: []string{"-I", "demo", "print"}, code: 0, stdout: "demo: 2\n",
		},
		{
			name: "builtin command replaced", program: "demo", modules: []string{"env"},
			args: []string{"env", "--x"}, code: 7, stdout: "external env --x\n",
		},
		{
			name: "builtin group replaced as a whole", program: "demo",
			modules: []string{"cluster"}, args: []string{"cluster", "show", "x", "--y"}, code: 7,
			stdout: "external cluster show x --y\n",
		},
		{
			name: "injected command replaced", program: "injected", modules: []string{"injected"},
			args: []string{"injected", "a", "-b"}, code: 7, stdout: "external injected a -b\n",
		},
		{
			name: "injected command kept with -I", program: "injected",
			modules: []string{"injected"}, args: []string{"-I", "injected", "a"}, code: 0,
			stdout: "internal a\n",
		},
		{
			name: "alias of a replaced command", program: "demo",
			modules: []string{"replicaset"}, args: []string{"rs", "status", "x"}, code: 7,
			stdout: "external replicaset status x\n",
		},
		{
			name: "module added", program: "demo", modules: []string{"hello"},
			args: []string{"-I", "hello", "a"}, code: 7, stdout: "external hello a\n",
		},
		{
			name: "version kept", program: "demo", modules: []string{"version"},
			args: []string{"version", "--short"}, code: 0,
			stdout: version.GetVersion(true, false) + "\n",
			stderr: func(dir string) string {
				return warning(`External module "version" (` + dir + `/version) is ignored: ` +
					`tt does not let a module replace the command "version"`)
			},
		},
		{
			name: "help kept", program: "demo", modules: []string{"help"},
			args: []string{"help", "errors"}, code: 0,
			stdout: "Exit codes of tt commands.\n", prefix: true,
			stderr: func(dir string) string {
				return warning(`External module "help" (` + dir + `/help) is ignored: ` +
					`tt does not let a module replace the command "help"`)
			},
		},
		{
			name: "module named modules", program: "demo", modules: []string{"modules", "hello"},
			args: []string{"modules", "list"}, code: 0, stdout: "hello - External hello\n",
			stderr: func(dir string) string {
				return warning(`External module "modules" (` + dir + `/modules) is ignored: ` +
					`tt does not let a module replace the command "modules"`)
			},
		},
		{
			name: "without external modules", program: "no-extmod", modules: []string{"demo"},
			args: []string{"demo", "print"}, code: 0, stdout: "demo: 2\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dir := writeExternalModules(t, testCase.modules...)

			got := runTT(t, ttRun{
				program: testCase.program, args: testCase.args,
				env: []string{"TT_CLI_MODULES_PATH=" + dir},
			})
			assert.Equal(t, testCase.code, got.code, got.stderr)

			if testCase.prefix {
				assert.True(t, strings.HasPrefix(got.stdout, testCase.stdout), got.stdout)
			} else {
				assert.Equal(t, testCase.stdout, got.stdout)
			}

			wantStderr := ""
			if testCase.stderr != nil {
				wantStderr = testCase.stderr(dir)
			}

			assert.Equal(t, wantStderr, got.stderr)
		})
	}
}

// TestMainReplacedCommandLogged checks that the debug log, which -V shows,
// names the command an external module replaces.
func TestMainReplacedCommandLogged(t *testing.T) {
	t.Parallel()

	dir := writeExternalModules(t, "env")

	got := runTT(t, ttRun{
		program: "demo", args: []string{"-V", "env", "--x"},
		env: []string{"TT_CLI_MODULES_PATH=" + dir},
	})
	require.Equal(t, 7, got.code, got.stderr)
	assert.Equal(t, "external env --x\n", got.stdout)
	assert.Contains(t, got.stderr, `   · External module "env" (`+dir+`/env/main) replaces `+
		`the command "env" of module "builtin"; run tt with -I to keep it`+"\n")
}

// TestMainExternalModulesListed checks where the external modules are
// listed - the root help, its completion and tt modules list - and that
// tt built without them has neither the modules nor tt modules.
func TestMainExternalModulesListed(t *testing.T) {
	t.Parallel()

	dir := writeExternalModules(t, "env", "hello", "version")
	env := []string{"TT_CLI_MODULES_PATH=" + dir}

	got := runTT(t, ttRun{program: "demo", args: []string{"--help"}, env: env})
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "EXTERNAL COMMANDS\n\x1b[0m  env\tExternal env\n"+
		"  hello\tExternal hello\n\x1b[0;1;39m\nFLAGS")
	assert.Contains(t, got.stdout, "\n  modules     Manage tt cli modules\n")

	got = runTT(t, ttRun{program: "demo", args: []string{"__complete", ""}, env: env})
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "\nhello\n")
	assert.Contains(t, got.stdout, "\ntt env\tExternal env\ntt hello\tExternal hello\n:0\n")

	got = runTT(t, ttRun{program: "demo", args: []string{"-I", "modules", "list"}, env: env})
	require.Equal(t, 0, got.code, got.stderr)
	assert.Equal(t, "env - External env\nhello - External hello\n", got.stdout)

	got = runTT(t, ttRun{program: "no-extmod", args: []string{"--help"}, env: env})
	require.Equal(t, 0, got.code, got.stderr)
	assert.NotContains(t, got.stdout, "EXTERNAL COMMANDS")
	assert.NotContains(t, got.stdout, "hello")
	assert.NotContains(t, got.stdout, "modules")
	assert.Empty(t, got.stderr)

	got = runTT(t, ttRun{program: "no-extmod", args: []string{"modules", "list"}, env: env})
	assert.Equal(t, 1, got.code)
	assert.Contains(t, got.stderr, "unknown command modules")
}
