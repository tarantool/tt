package coredump

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testTime is the time of the dump of the tests, --time.
const testTime = "1763869440"

// fakeProcess makes the directory of the process pid in the procfs at proc:
// its comm and its exe, a symbolic link to exe.
func fakeProcess(t *testing.T, proc string, pid int, comm, exe string) {
	t.Helper()

	dir := filepath.Join(proc, strconv.Itoa(pid))

	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
	require.NoError(t, os.Symlink(exe, filepath.Join(dir, "exe")))
}

// testCoreData returns the bytes of a core on the standard input: data,
// blocks of zeros, as Linux pipes the pages a process never touched, and
// data again, then zeros to the end.
func testCoreData() []byte {
	var data bytes.Buffer

	data.WriteString("\x7fELF core")
	data.Write(make([]byte, 4*sparseBlock))
	data.WriteString("the stack")
	data.Write(make([]byte, sparseBlock+123))

	return data.Bytes()
}

// keepsHoles reports whether the file system of dir keeps a hole of gap
// bytes between two blocks of data, of a file written as writeSparse
// writes one: a block, a seek over the gap from its end, and a block; the
// file is kept sparse when it takes less of the disk than its size. The
// file systems of Linux keep a hole of a block, ext4, XFS, Btrfs and tmpfs
// among them, and there one that does not fails the test, so that the
// check of the holes is never dropped unseen. Elsewhere it is the file
// system's own: the APFS of the macOS runner of CI allocated the zeros of a
// gap of 12 KiB and of one of a MiB, and kept a hole of 16 MiB, so the
// probe is of the gap the test writes, and where it is not kept, the test
// passes over its check of the holes and says so.
func keepsHoles(t *testing.T, dir string, gap int64) bool {
	t.Helper()

	path := filepath.Join(dir, "holes")
	block := bytes.Repeat([]byte{1}, sparseBlock)

	probe, err := os.Create(path)
	require.NoError(t, err)

	_, err = probe.Write(block)
	require.NoError(t, err)

	_, err = probe.Seek(gap, io.SeekCurrent)
	require.NoError(t, err)

	_, err = probe.Write(block)
	require.NoError(t, err)
	require.NoError(t, probe.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)

	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.NoError(t, os.Remove(path))

	if stat.Blocks*512 < info.Size() {
		return true
	}

	if runtime.GOOS == "linux" {
		t.Fatalf("the file system of %s keeps no hole of %d bytes: those of Linux do, and "+
			"the holes are checked there", dir, gap)
	}

	t.Logf("the file system of %s keeps no hole of %d bytes: the holes are not checked",
		dir, gap)

	return false
}

// panicReader is a standard input that must not be read.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("the standard input is read")
}

func TestReceiveCore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	tarantool := testExecutable(t, testExecBase, 0x123, testBuildID).write(t,
		filepath.Join(root, "bin", "tarantool"))
	sleep := filepath.Join(root, "bin", "sleep")

	require.NoError(t, os.WriteFile(sleep, []byte("#!/bin/sh\n"), 0o755))
	fakeProcess(t, proc, 42, "tarantool", tarantool)
	fakeProcess(t, proc, 43, "sleep", sleep)
	fakeProcess(t, proc, 44, "a/b", sleep)

	data := testCoreData()

	cases := []struct {
		name       string
		opts       PackOptions
		executable string
		core       string
		tarantool  bool
	}{
		{
			name:       "pipe",
			opts:       PackOptions{Core: "", Executable: "", Dir: "", PID: 42, Time: testTime},
			executable: tarantool, core: "tarantool-core.42." + testTime, tarantool: true,
		},
		{
			name:       "stdin",
			opts:       PackOptions{Core: StdinCore, Executable: "", Dir: "", PID: 42, Time: "1"},
			executable: tarantool, core: "tarantool-core.42.1", tarantool: true,
		},
		{
			name: "executable",
			opts: PackOptions{
				Core: "", Executable: "/opt/tarantool", Dir: "", PID: 42, Time: "2",
			},
			executable: "/opt/tarantool", core: "tarantool-core.42.2", tarantool: true,
		},
		{
			name:       "not a tarantool",
			opts:       PackOptions{Core: "", Executable: "", Dir: "", PID: 43, Time: testTime},
			executable: sleep, core: "sleep-core.43." + testTime, tarantool: false,
		},
		{
			name:       "slash",
			opts:       PackOptions{Core: "", Executable: "", Dir: "", PID: 44, Time: testTime},
			executable: sleep, core: "a_b-core.44." + testTime, tarantool: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "var", "core")
			require.NoError(t, os.MkdirAll(dir, 0o755))

			opts := testCase.opts

			opts.Dir = dir

			got, tarantool, err := receiveCore(bytes.NewReader(data), opts, proc)
			require.NoError(t, err)
			assert.Equal(t, testCase.tarantool, tarantool)

			want := opts

			want.Core = filepath.Join(dir, testCase.core)
			want.Executable = testCase.executable
			assert.Equal(t, want, got)

			stored, err := os.ReadFile(got.Core)
			require.NoError(t, err)
			assert.Equal(t, data, stored, "the core of the standard input, byte for byte")

			info, err := os.Stat(got.Core)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(coreFileMode), info.Mode().Perm())

			// The zeros are holes: the file takes less than its size, where
			// the file system keeps a hole of the three blocks of zeros
			// between the data.
			if keepsHoles(t, dir, 3*sparseBlock) {
				stat, ok := info.Sys().(*syscall.Stat_t)
				require.True(t, ok)
				assert.Less(t, stat.Blocks*512, info.Size())
			}
		})
	}
}

func TestReceiveCoreTime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proc := filepath.Join(root, "proc")

	fakeProcess(t, proc, 42, "tarantool", "/usr/bin/tarantool")

	before := time.Now().Unix()

	got, _, err := receiveCore(strings.NewReader("core"), PackOptions{
		Core: "", Executable: "", Dir: root, PID: 42, Time: "",
	}, proc)
	require.NoError(t, err)

	// The time of the dump is now, and names the core and the archive.
	dumped, err := strconv.ParseInt(got.Time, 10, 64)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dumped, before)
	assert.LessOrEqual(t, dumped, time.Now().Unix())
	assert.Equal(t, filepath.Join(root, "tarantool-core.42."+got.Time), got.Core)
}

func TestReceiveCoreFile(t *testing.T) {
	t.Parallel()

	// A core of a file is packed as it is, procfs and the standard input
	// unread: its process is gone, and the PID names the archive alone.
	opts := PackOptions{
		Core: "/var/core/core.42", Executable: "", Dir: "", PID: 42, Time: testTime,
	}

	got, tarantool, err := receiveCore(panicReader{}, opts, filepath.Join(t.TempDir(), "proc"))
	require.NoError(t, err)
	assert.True(t, tarantool)
	assert.Equal(t, opts, got)
}

func TestReceiveCoreErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	proc := filepath.Join(root, "proc")

	fakeProcess(t, proc, 42, "tarantool", "/usr/bin/tarantool")

	errRead := errors.New("the pipe is broken")

	cases := []struct {
		name  string
		stdin io.Reader
		pid   uint
		err   error
		msg   string
	}{
		{
			name: "no process", stdin: panicReader{}, pid: 43, err: errNoProcess,
			msg: "no process of the core on the standard input: no " +
				filepath.Join(proc, "43") + ": the core on the standard input is that of a " +
				"process of procfs, as a kernel.core_pattern pipe gives it; give COREDUMP as a " +
				"file otherwise",
		},
		{
			name: "no PID", stdin: panicReader{}, pid: 0, err: errNoProcess,
			msg: "no " + filepath.Join(proc, "0") + ": ",
		},
		{name: "no stdin", stdin: nil, pid: 42, err: errNoStdin, msg: ""},
		{
			name: "read", pid: 42, err: errRead,
			stdin: io.MultiReader(strings.NewReader("\x7fELF core"), iotest.ErrReader(errRead)),
			msg:   "cannot store the core on the standard input to ",
		},
	}

	for _, testCase := range cases {
		dir := t.TempDir()

		_, _, err := receiveCore(testCase.stdin, PackOptions{
			Core: "", Executable: "", Dir: dir, PID: testCase.pid, Time: testTime,
		}, proc)
		require.ErrorIs(t, err, testCase.err, testCase.name)
		assert.Contains(t, err.Error(), testCase.msg, testCase.name)

		// Nothing is left of a core that is not stored whole.
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries, testCase.name)
	}

	// A core of the name is not overwritten.
	dir := t.TempDir()
	core := filepath.Join(dir, "tarantool-core.42."+testTime)

	require.NoError(t, os.WriteFile(core, []byte("the first core"), 0o600))

	_, _, err := receiveCore(strings.NewReader("the second"), PackOptions{
		Core: "", Executable: "", Dir: dir, PID: 42, Time: testTime,
	}, proc)
	require.ErrorIs(t, err, os.ErrExist)

	content, err := os.ReadFile(core)
	require.NoError(t, err)
	assert.Equal(t, "the first core", string(content))
}

func TestProcessExecutable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tarantool := filepath.Join(root, "tarantool")

	require.NoError(t, os.WriteFile(tarantool, []byte("#!/bin/sh\n"), 0o755))

	// The path the exe links to, when it is the file of the link.
	fakeProcess(t, root, 1, "tarantool", tarantool)
	assert.Equal(t, tarantool, processExecutable(filepath.Join(root, "1")))

	// The link itself, when its path is of no file, as one deleted since it
	// was run, " (deleted)", is.
	deleted := filepath.Join(root, "2")

	fakeProcess(t, root, 2, "tarantool", tarantool+" (deleted)")
	assert.Equal(t, filepath.Join(deleted, "exe"), processExecutable(deleted))

	// So it is when the path is of another file than the link, as a path of
	// another mount namespace is: here a relative one, which the link
	// resolves against its directory, to a copy of pipe.go, and the path
	// against the working directory of the test, to pipe.go of the package.
	other := filepath.Join(root, "3")

	fakeProcess(t, root, 3, "tarantool", "pipe.go")
	require.NoError(t, os.WriteFile(filepath.Join(other, "pipe.go"), []byte("package x\n"), 0o644))
	require.FileExists(t, "pipe.go")
	assert.Equal(t, filepath.Join(other, "exe"), processExecutable(other))

	// No exe at all.
	assert.Equal(t, filepath.Join(root, "4", "exe"), processExecutable(filepath.Join(root, "4")))
}
