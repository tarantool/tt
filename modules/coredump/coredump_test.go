package coredump_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/coredump"
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
	assert.Regexp(t, `\n  pack\s+pack tarantool coredump into tar.gz archive\n`, got.Stdout)
	assert.Regexp(t, `\n  unpack\s+unpack tarantool coredump tar.gz archive\n`, got.Stdout)
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
