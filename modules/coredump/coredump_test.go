package coredump_test

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/coredump"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// writeArchive writes a tar.gz archive with the files of contents, by name,
// into dir and returns its path.
func writeArchive(t *testing.T, dir, name string, contents map[string]string) string {
	t.Helper()

	path := filepath.Join(dir, name)

	archive, err := os.Create(path)
	require.NoError(t, err)

	compressed := gzip.NewWriter(archive)
	writer := tar.NewWriter(compressed)

	for member, content := range contents {
		require.NoError(t, writer.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: member, Mode: 0o644, Size: int64(len(content)),
		}))

		_, err := writer.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, archive.Close())

	return path
}

func TestCommands(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "--help")
	require.NoError(t, got.Err)
	assert.Regexp(t, `\n  inspect\s+inspect tarantool coredump\n`, got.Stdout)
	assert.Regexp(t, `\n  list\s+list the crashes of instances and where their cores are\n`,
		got.Stdout)
	assert.Regexp(t, `\n  pack\s+pack tarantool coredump into tar.gz archive\n`, got.Stdout)
	assert.Regexp(t, `\n  unpack\s+unpack tarantool coredump tar.gz archive\n`, got.Stdout)
}

// TestList lists the crashes of the instances the services give, each with
// where its core is, in a table and in JSON, and says which instances have
// none. The crashes dumped no core: where the cores of the others are is
// what kernel.core_pattern of the host says, which the unit tests of the
// locator pin.
func TestList(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	instances := sdktest.WithLocalInstances(
		sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir(), Crashes: []sdk.Crash{
			{At: at.Add(-time.Hour), PID: 41, Signal: syscall.SIGABRT, CoreDumped: false},
			{At: at, PID: 42, Signal: syscall.SIGKILL, CoreDumped: false},
		}},
		sdk.LocalInstance{Name: "app:i2", WorkDir: t.TempDir()},
	)
	services := sdktest.New(t, instances)

	got := services.Run(coredump.New, "coredump", "list", "--scope", "user", "app:*")
	require.NoError(t, got.Err)
	assert.Equal(t, "INSTANCE  CRASHED AT           PID  SIGNAL   CORE\n"+
		"app:i1    2026-11-24 10:00:00  42   SIGKILL  none dumped\n"+
		"app:i1    2026-11-24 09:00:00  41   SIGABRT  none dumped\n", got.Stdout)
	require.NotEmpty(t, got.Records)
	assert.Equal(t, "No crash is recorded of app:i2.", got.Records[0].Message)

	calls := services.LocalInstancesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"app:*"}, calls[0].Targets)
	assert.Equal(t, sdk.AppLookup{Scope: "user", Template: ""}, calls[0].Lookup)

	got = sdktest.New(t, instances).Run(coredump.New, "coredump", "list", "-o", "json", "app:i1")
	require.NoError(t, got.Err)

	var listed []map[string]any

	require.NoError(t, json.Unmarshal([]byte(got.Stdout), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "app:i1", listed[0]["instance"])

	crashes, ok := listed[0]["crashes"].([]any)
	require.True(t, ok)
	require.Len(t, crashes, 2)

	last, ok := crashes[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 42, last["pid"], 0)
	assert.InDelta(t, 9, last["signal"], 0)
	assert.Equal(t, "SIGKILL", last["signal_name"])
	assert.Equal(t, false, last["core_dumped"])
	assert.NotContains(t, last, "core")
	assert.Contains(t, last["missing"], "no core was dumped")

	got = sdktest.New(t, instances).Run(coredump.New, "coredump", "list", "app:none")
	require.ErrorIs(t, got.Err, sdk.ErrNotFound)

	// Crashes that cannot be read are warned of, with why, and are no lack
	// of crashes; -o json gives why.
	unreadable := sdktest.WithLocalInstances(
		sdk.LocalInstance{Name: "app:i3", WorkDir: t.TempDir(),
			CrashesErr: errors.New("cannot read the crashes: open /run/app/i3/crashes.json: " +
				"permission denied")},
		sdk.LocalInstance{Name: "app:i2", WorkDir: t.TempDir()},
	)

	got = sdktest.New(t, unreadable).Run(coredump.New, "coredump", "list")
	require.NoError(t, got.Err)
	assert.Empty(t, got.Stdout)
	require.Len(t, got.Records, 2)
	assert.Equal(t, slog.LevelWarn, got.Records[0].Level)
	assert.Equal(t, "The crashes of app:i3 cannot be read: cannot read the crashes: open "+
		"/run/app/i3/crashes.json: permission denied.", got.Records[0].Message)
	assert.Equal(t, "No crash is recorded of app:i2.", got.Records[1].Message)

	got = sdktest.New(t, unreadable).Run(coredump.New, "coredump", "list", "-o", "json")
	require.NoError(t, got.Err)
	require.NoError(t, json.Unmarshal([]byte(got.Stdout), &listed))
	assert.Equal(t, "cannot read the crashes: open /run/app/i3/crashes.json: permission denied",
		listed[0]["crashes_error"])
	assert.NotContains(t, listed[1], "crashes_error")
}

// TestPackInstance packs by an instance target, which is no file: the
// instance is resolved as tt instance status resolves it, with --scope, and
// a crash with no core, an instance with none, a target of two instances
// and one not found fail, saying why and where to look.
func TestPackInstance(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	instances := sdktest.WithLocalInstances(
		sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir(), Crashes: []sdk.Crash{
			{At: at, PID: 42, Signal: syscall.SIGKILL, CoreDumped: false},
		}},
		sdk.LocalInstance{Name: "app:i2", WorkDir: t.TempDir()},
	)
	services := sdktest.New(t, instances)

	got := services.Run(coredump.New, "coredump", "pack", "--scope", "user", "app:i1")
	require.ErrorContains(t, got.Err, "app:i1: no core was dumped by any of its 1 crashes: the "+
		"last, PID 42, SIGKILL, at 2026-11-24 10:00:00")
	require.ErrorContains(t, got.Err, "; tt coredump list app:i1 lists the crashes of the "+
		"instance and where their cores are")

	calls := services.LocalInstancesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"app:i1"}, calls[0].Targets)
	assert.Equal(t, sdk.AppLookup{Scope: "user", Template: ""}, calls[0].Lookup)

	got = sdktest.New(t, instances).Run(coredump.New, "coredump", "pack", "app:i2")
	require.ErrorContains(t, got.Err, "app:i2: no crash is recorded")
	require.ErrorContains(t, got.Err, "tt coredump list app:i2")

	got = sdktest.New(t, instances).Run(coredump.New, "coredump", "pack", "app:*")
	require.ErrorContains(t, got.Err, "app:* selects 2 instances: pack takes the core of one "+
		"instance, <APP>:<INSTANCE>")
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())

	got = sdktest.New(t, instances).Run(coredump.New, "coredump", "pack", "app:none")
	require.ErrorIs(t, got.Err, sdk.ErrNotFound)
}

// TestPackInstanceFileOfTheName packs a file of the working directory named
// as an instance, app:i1, given by that name: it is a core, whatever its
// name, and no instance target. The name is relative: an absolute path has
// a slash, which no instance target has.
//
//nolint:paralleltest // Changes the working directory.
func TestPackInstanceFileOfTheName(t *testing.T) {
	instances := sdktest.WithLocalInstances(sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir()})

	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("app:i1", []byte("not a core"), 0o600))

	services := sdktest.New(t, instances)

	got := services.Run(coredump.New, "coredump", "pack", "-e", "/bin/true", "app:i1")
	require.ErrorContains(t, got.Err, "not a core dump: app:i1")
	assert.Empty(t, services.LocalInstancesCalls())
}

func TestArguments(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"pack", "unpack", "inspect"} {
		got := sdktest.Run(t, coredump.New, "coredump", command)
		require.ErrorContains(t, got.Err, "accepts 1 arg(s), received 0", command)
	}
}

// noPID is a PID of no process: above 2^22, the highest pid_max of Linux.
const noPID = "4194305"

func TestPackArguments(t *testing.T) {
	t.Parallel()

	core := filepath.Join(t.TempDir(), "core")

	cases := []struct {
		name string
		args []string
		msgs []string
	}{
		{
			name: "stdin without --pid", args: []string{"-"},
			msgs: []string{"COREDUMP -: the core on the standard input needs --pid"},
		},
		{
			name: "two cores", args: []string{"-p", "42", core, core},
			msgs: []string{"accepts at most 1 arg(s), received 2"},
		},
		{
			// The core on the standard input is that of a process of procfs.
			name: "no process", args: []string{"-p", noPID},
			msgs: []string{"no process of the core on the standard input: no /proc/" + noPID},
		},
		{
			name: "no process, stdin", args: []string{"-p", noPID, "-"},
			msgs: []string{"no process of the core on the standard input: no /proc/" + noPID},
		},
		{
			// A core of a file is that file, whatever the PID.
			name: "file", args: []string{"-p", noPID, "-e", "/bin/true", core},
			msgs: []string{"not a core dump: " + core + ": ", "no such file or directory"},
		},
	}

	for _, testCase := range cases {
		args := append([]string{"coredump", "pack"}, testCase.args...)

		got := sdktest.New(t, sdktest.WithStdin(strings.NewReader("core"))).Run(coredump.New,
			args...)
		for _, msg := range testCase.msgs {
			require.ErrorContains(t, got.Err, msg, testCase.name)
		}
	}
}

// TestPackPipeNotTarantool runs pack as a kernel.core_pattern pipe for a
// process that is not a tarantool, the test itself: its core, on the
// standard input, is stored as <comm>-core.<PID>.<TIME> in --dir and not
// packed, and pack succeeds.
func TestPackPipeNotTarantool(t *testing.T) {
	t.Parallel()

	comm, err := os.ReadFile("/proc/self/comm")
	if err != nil {
		t.Skipf("needs procfs: %v", err)
	}

	pid := strconv.Itoa(os.Getpid())

	for _, args := range [][]string{{}, {"-"}} {
		dir := t.TempDir()
		core := filepath.Join(dir, strings.TrimSpace(string(comm))+"-core."+pid+".1763869440")

		got := sdktest.New(t, sdktest.WithStdin(strings.NewReader("the core"))).Run(
			coredump.New, append([]string{
				"coredump", "pack", "-p", pid, "-d", dir, "-t", "1763869440",
			}, args...)...)
		require.NoError(t, got.Err)
		require.Len(t, got.Records, 1)
		assert.Equal(t, "The process "+pid+" is not a tarantool, by its comm: its core is "+
			"stored and not packed: "+core, got.Records[0].Message)

		content, err := os.ReadFile(core)
		require.NoError(t, err)
		assert.Equal(t, "the core", string(content))

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "the core alone, no archive")
	}
}

func TestPackNoSuchFile(t *testing.T) {
	t.Parallel()

	core := filepath.Join(t.TempDir(), "core")

	got := sdktest.Run(t, coredump.New, "coredump", "pack", "-e", "/bin/true", core)
	require.ErrorContains(t, got.Err, "not a core dump: "+core+": ")
	require.ErrorContains(t, got.Err, "no such file or directory")
}

//nolint:paralleltest // Unpack extracts into the working directory.
func TestUnpack(t *testing.T) {
	dir := t.TempDir()
	archive := writeArchive(t, dir, "tarantool-core-N-202611230344-host.tar.gz",
		map[string]string{
			"tarantool-core-N-202611230344-host/version": "Tarantool 3.9.0-0-g8b9a0f59a95b\n",
		})

	t.Chdir(dir)

	got := sdktest.Run(t, coredump.New, "coredump", "unpack", archive)
	require.NoError(t, got.Err)
	assert.Equal(t, "Archive was successfully unpacked.", got.Records[0].Message)

	content, err := os.ReadFile(filepath.Join(dir, "tarantool-core-N-202611230344-host",
		"version"))
	require.NoError(t, err)
	assert.Equal(t, "Tarantool 3.9.0-0-g8b9a0f59a95b\n", string(content))

	got = sdktest.Run(t, coredump.New, "coredump", "unpack", filepath.Join(dir, "missing"))
	require.ErrorContains(t, got.Err, "failed to unpack")
}

func TestInspectNoSuchFile(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "inspect", filepath.Join(t.TempDir(), "x"))
	require.ErrorContains(t, got.Err, "failed to inspect")
}

// TestInspectExperimental checks that inspect takes --experimental and goes
// on to open what it is given with it.
func TestInspectExperimental(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "inspect", "--experimental",
		filepath.Join(t.TempDir(), "x"))
	require.ErrorContains(t, got.Err, "failed to inspect")
}
