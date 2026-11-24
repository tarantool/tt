package coredump

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
)

// emptyPaths are the ways PATH is empty, as the kernel runs the pipe of
// kernel.core_pattern with no environment: unset and set to nothing. Each
// sets PATH so for the rest of the test.
var emptyPaths = map[string]func(t *testing.T){
	"unset": func(t *testing.T) {
		t.Helper()
		t.Setenv("PATH", "")
		require.NoError(t, os.Unsetenv("PATH"))
	},
	"empty": func(t *testing.T) {
		t.Helper()
		t.Setenv("PATH", "")
	},
}

// TestLookPath checks that lookPath finds a program in PATH, as
// exec.LookPath does, and with PATH empty or unset in the PATH of the
// system: sh there, and an error of exec.ErrNotFound that names the PATH
// of the system for a program it has not. A name with a slash is not
// looked up.
//
//nolint:paralleltest // Sets PATH.
func TestLookPath(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		bin := t.TempDir()
		program := filepath.Join(bin, "tt-test-program")

		require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("PATH", bin)

		path, err := lookPath("tt-test-program")
		require.NoError(t, err)
		assert.Equal(t, program, path)

		_, err = lookPath("sh")
		require.ErrorIs(t, err, exec.ErrNotFound)
	})

	for name, setPath := range emptyPaths {
		t.Run(name, func(t *testing.T) {
			setPath(t)

			path, err := lookPath("sh")
			require.NoError(t, err)
			assert.Contains(t, filepath.SplitList(defaultSearchPath), filepath.Dir(path))
			assert.Equal(t, "sh", filepath.Base(path))

			_, err = lookPath("tt-test-no-such-program")
			require.ErrorIs(t, err, exec.ErrNotFound)
			require.EqualError(t, err, `exec: "tt-test-no-such-program": executable file not `+
				`found in $PATH, which is empty, nor in `+defaultSearchPath)

			program := filepath.Join(t.TempDir(), "tt-test-program")
			require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755))

			path, err = lookPath(program)
			require.NoError(t, err)
			assert.Equal(t, program, path)
		})
	}
}

// TestCommandEnv checks the environment of a program tt runs: that of tt
// with a PATH that is not empty, and that of tt with the PATH of the system
// otherwise, with no PATH of its own left.
func TestCommandEnv(t *testing.T) {
	t.Parallel()

	environ := []string{"HOME=/root", "PATH=/opt/bin", "LANG=C"}
	assert.Equal(t, environ, commandEnv(environ))

	assert.Equal(t, []string{"PATH=" + defaultSearchPath}, commandEnv(nil))
	assert.Equal(t, []string{"HOME=/root", "LANG=C", "PATH=" + defaultSearchPath},
		commandEnv([]string{"HOME=/root", "PATH=", "LANG=C"}))
	assert.Equal(t, []string{"PATHS=/opt", "PATH=" + defaultSearchPath},
		commandEnv([]string{"PATHS=/opt"}))
}

// TestCommandEmptyPath runs env with PATH empty or unset, as the kernel runs
// the pipe of kernel.core_pattern: it is found in the PATH of the system, and
// has that PATH in its environment, for the programs it runs, as those of
// the pack script are; its first argument is its name, as of a program
// found in PATH. A program the PATH of the system has not fails to start,
// with the error of the lookup.
//
//nolint:paralleltest // Sets PATH.
func TestCommandEmptyPath(t *testing.T) {
	for name, setPath := range emptyPaths {
		t.Run(name, func(t *testing.T) {
			setPath(t)

			cmd := command(t.Context(), "env")
			require.NoError(t, cmd.Err)
			assert.True(t, filepath.IsAbs(cmd.Path), cmd.Path)
			assert.Equal(t, "env", cmd.Args[0])

			out, err := cmd.Output()
			require.NoError(t, err)
			assert.Contains(t, strings.Split(string(out), "\n"), "PATH="+defaultSearchPath)

			err = command(t.Context(), "tt-test-no-such-program").Run()
			require.ErrorIs(t, err, exec.ErrNotFound)
			require.ErrorContains(t, err, "nor in "+defaultSearchPath)
		})
	}
}

// TestCommandPath runs a program of PATH, set, as exec.CommandContext does:
// the environment is that of tt, and a program PATH has not fails to start.
//
//nolint:paralleltest // Sets PATH.
func TestCommandPath(t *testing.T) {
	bin := t.TempDir()
	program := filepath.Join(bin, "tt-test-program")

	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\necho \"$PATH\"\n"), 0o755))
	t.Setenv("PATH", bin)

	cmd := command(t.Context(), "tt-test-program")
	assert.Equal(t, program, cmd.Path)
	assert.Equal(t, os.Environ(), cmd.Env)

	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, bin+"\n", string(out))

	err = command(t.Context(), "sh").Run()
	require.ErrorIs(t, err, exec.ErrNotFound)
	require.NotContains(t, err.Error(), defaultSearchPath)
}

// TestPackScriptEmptyPath runs the pack script as Pack does, with PATH empty
// or unset, as the kernel runs the pipe of kernel.core_pattern: it packs the
// core. Pack found no bash for the script there, `exec: "bash": executable
// file not found in $PATH`, with the core of the pipe stored and not packed.
//
//nolint:paralleltest // Sets PATH.
func TestPackScriptEmptyPath(t *testing.T) {
	requirePackScript(t)

	for name, setPath := range emptyPaths {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			out := t.TempDir()
			core := filepath.Join(dir, "core")

			require.NoError(t, os.WriteFile(core, []byte("the core"), 0o600))

			args, err := packArgs(PackOptions{
				Core: core, Executable: fakeTarantool(t, dir), Dir: out, PID: 42,
				Time: "1763869440",
			})
			require.NoError(t, err)

			report := filepath.Join(t.TempDir(), archiveReport)

			setPath(t)

			var stderr strings.Builder

			err = runPackScript(t.Context(), output.Streams{In: nil, Out: io.Discard, Err: &stderr},
				t.TempDir(), append(args, "-r", report))
			require.NoError(t, err, stderr.String())

			archives, err := filepath.Glob(filepath.Join(out, "tarantool-core-42-*.tar.gz"))
			require.NoError(t, err)
			require.Len(t, archives, 1)

			reported, err := os.ReadFile(report)
			require.NoError(t, err)
			assert.Equal(t, archives[0]+"\n", string(reported))
			assert.Equal(t, "the core", archiveMembers(t, archives[0])["coredump"])
		})
	}
}

// TestGDBProbeEmptyPath probes gdb with PATH empty or unset: the gdb of the
// PATH of the system is found, where inspect failed, gdb not found in PATH.
//
//nolint:paralleltest // Sets PATH.
func TestGDBProbeEmptyPath(t *testing.T) {
	gdb := ""

	for _, dir := range filepath.SplitList(defaultSearchPath) {
		path := filepath.Join(dir, debuggerGDB)

		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			gdb = path

			break
		}
	}

	if gdb == "" {
		t.Skip("no gdb in " + defaultSearchPath)
	}

	for name, setPath := range emptyPaths {
		t.Run(name, func(t *testing.T) {
			setPath(t)

			probe, err := gdbDebugger{}.Probe(t.Context())
			require.NoError(t, err)
			assert.Equal(t, gdb, probe.Path)

			cmd, err := gdbDebugger{}.Command(t.Context(), t.TempDir(), t.TempDir(), "", nil)
			require.NoError(t, err)
			require.NoError(t, cmd.Err)
			assert.True(t, filepath.IsAbs(cmd.Path), cmd.Path)
			assert.Equal(t, "sh", cmd.Args[0])
			assert.True(t, slices.Contains(cmd.Env, "PATH="+defaultSearchPath), cmd.Env)
		})
	}
}
