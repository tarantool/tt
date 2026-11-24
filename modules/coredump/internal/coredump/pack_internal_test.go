package coredump

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

//nolint:paralleltest // Changes the working directory.
func TestPackArgs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// The working directory as the process sees it: t.TempDir may be under
	// a symbolic link.
	cwd, err := filepath.Abs(".")
	require.NoError(t, err)

	args, err := packArgs(PackOptions{
		Core: "core", Executable: "bin/tarantool", Dir: "../out", PID: 42, Time: "1763869440",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"-c", filepath.Join(cwd, "core"),
		"-e", filepath.Join(cwd, "bin", "tarantool"),
		"-d", filepath.Join(filepath.Dir(cwd), "out"),
		"-p", "42",
		"-t", "1763869440",
	}, args)

	args, err = packArgs(PackOptions{Core: "/var/lib/systemd/coredump/core", Executable: "",
		Dir: "", PID: 0, Time: ""})
	require.NoError(t, err)
	assert.Equal(t, []string{"-c", "/var/lib/systemd/coredump/core"}, args)
}

// requirePackScript skips a test that runs the pack script where it does
// not run: it needs bash and the getopt, date and tar of GNU.
func requirePackScript(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("bash")
	if runtime.GOOS != "linux" || err != nil {
		t.Skip("the pack script needs bash and the getopt, date and tar of GNU, on Linux")
	}
}

// fakeTarantool writes a tarantool into dir that prints its version, all
// the pack script runs it for, and returns its path.
func fakeTarantool(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "tarantool")

	require.NoError(t, os.WriteFile(path,
		[]byte("#!/bin/sh\necho 'Tarantool 3.9.0-0-g8b9a0f59a95b'\n"), 0o755))

	return path
}

// runScript runs the pack script on its own, with stdin its standard input
// and args, and returns its output.
func runScript(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	script := filepath.Join(t.TempDir(), "tarabrt.sh")

	require.NoError(t, copyEmbedded(coreScripts, packEmbedPath, script, scriptFileMode))

	cmd := exec.CommandContext(t.Context(), "bash", append([]string{script}, args...)...)

	cmd.Stdin = strings.NewReader(stdin)

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// archiveMembers returns the contents of the members of the tar.gz archive
// at path by their names, without the directory of the archive.
func archiveMembers(t *testing.T, path string) map[string]string {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	compressed, err := gzip.NewReader(file)
	require.NoError(t, err)

	members := map[string]string{}
	reader := tar.NewReader(compressed)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		content, err := io.ReadAll(reader)
		require.NoError(t, err)

		_, name, _ := strings.Cut(header.Name, "/")

		members[name] = string(content)
	}

	return members
}

// TestPackScriptFiles runs the pack script as Pack does with the files of
// -f: a path, packed under it, and NAME=PATH, packed as NAME, as the debug
// file of a debug directory of the host is packed into usr/lib/debug.
func TestPackScriptFiles(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	out := t.TempDir()
	core := filepath.Join(dir, "core")
	library := filepath.Join(dir, "lib", "libm.so.6")
	debugFile := filepath.Join(dir, "debug", ".build-id", "ab", "cdef.debug")
	member := "usr/lib/debug/.build-id/ab/cdef.debug"

	require.NoError(t, os.MkdirAll(filepath.Dir(library), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(debugFile), 0o755))
	require.NoError(t, os.WriteFile(core, []byte("the core"), 0o600))
	require.NoError(t, os.WriteFile(library, []byte("a library"), 0o644))
	require.NoError(t, os.WriteFile(debugFile, []byte("the debug file"), 0o644))

	args, err := packArgs(PackOptions{
		Core: core, Executable: fakeTarantool(t, dir), Dir: out, PID: 0, Time: "1763869440",
	})
	require.NoError(t, err)

	var stderr strings.Builder

	err = runPackScript(output.Streams{In: nil, Out: io.Discard, Err: &stderr}, t.TempDir(),
		append(args, "-f", library, "-f", member+"="+debugFile))
	require.NoError(t, err, stderr.String())

	archives, err := filepath.Glob(filepath.Join(out, "*.tar.gz"))
	require.NoError(t, err)
	require.Len(t, archives, 1)

	members := archiveMembers(t, archives[0])
	assert.Equal(t, "a library", members[strings.TrimPrefix(library, "/")])
	assert.Equal(t, "the debug file", members[member])
	assert.Equal(t, "the core", members["coredump"])
}

// TestPackScriptFailure runs the pack script as Pack does, and checks that
// a pack that fails leaves nothing in the directory of the archive: neither
// the temporary directory of the script nor a partial archive, which both
// stayed there. The version of the tarantool fails, before the archive is
// begun, or tar fails on a file it cannot read, a missing one for root,
// after it has begun the archive. An archive packed before of the name the
// failed pack would take stays, and the failed pack leaves no archive of the
// name it took instead.
func TestPackScriptFailure(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	failing := filepath.Join(dir, "failing")
	unreadable := filepath.Join(dir, "unreadable.so")

	require.NoError(t, os.WriteFile(core, []byte("the core"), 0o600))
	require.NoError(t, os.WriteFile(failing, []byte("#!/bin/sh\nexit 3\n"), 0o755))
	require.NoError(t, os.WriteFile(unreadable, []byte("a library"), 0o000))

	tarantool := fakeTarantool(t, dir)

	cases := []struct {
		name, executable string
		files            []string
	}{
		{name: "version", executable: failing, files: nil},
		{name: "missing", executable: tarantool, files: []string{filepath.Join(dir, "gone.so")}},
	}

	if os.Geteuid() != 0 {
		cases = append(cases, struct {
			name, executable string
			files            []string
		}{name: "unreadable", executable: tarantool, files: []string{unreadable}})
	}

	for _, testCase := range cases {
		out := t.TempDir()

		args, err := packArgs(PackOptions{
			Core: core, Executable: testCase.executable, Dir: out, PID: 42, Time: "1763869440",
		})
		require.NoError(t, err)

		for _, file := range testCase.files {
			args = append(args, "-f", file)
		}

		var stderr strings.Builder

		err = runPackScript(output.Streams{In: nil, Out: io.Discard, Err: &stderr}, t.TempDir(),
			args)
		require.Error(t, err, testCase.name)

		entries, err := os.ReadDir(out)
		require.NoError(t, err)
		assert.Empty(t, entries, "%s: %s", testCase.name, stderr.String())

		// The pack succeeds once the file is there, and the archive stays
		// when the next pack of its name fails.
		for _, file := range testCase.files {
			require.NoError(t, os.RemoveAll(file))
			require.NoError(t, os.WriteFile(file, []byte("a library"), 0o644))
		}

		args, err = packArgs(PackOptions{
			Core: core, Executable: tarantool, Dir: out, PID: 42, Time: "1763869440",
		})
		require.NoError(t, err)
		require.NoError(t, runPackScript(output.Streams{In: nil, Out: io.Discard, Err: io.Discard},
			t.TempDir(), args), testCase.name)

		archives, err := filepath.Glob(filepath.Join(out, "*.tar.gz"))
		require.NoError(t, err)
		require.Len(t, archives, 1, testCase.name)

		packed, err := os.ReadFile(archives[0])
		require.NoError(t, err)

		args, err = packArgs(PackOptions{
			Core: core, Executable: failing, Dir: out, PID: 42, Time: "1763869440",
		})
		require.NoError(t, err)
		require.Error(t, runPackScript(output.Streams{In: nil, Out: io.Discard, Err: io.Discard},
			t.TempDir(), args), testCase.name)

		entries, err = os.ReadDir(out)
		require.NoError(t, err)
		require.Len(t, entries, 1, testCase.name)

		content, err := os.ReadFile(archives[0])
		require.NoError(t, err)
		assert.Equal(t, packed, content, testCase.name)
	}
}

// TestPackScriptReport runs the pack script with -r, as Pack runs it: the
// script writes the path of the archive into the file of -r, with a
// standard output that is no terminal, where it tells the path to a
// terminal alone. The archive is named by the PID of -p, with the core of
// -c, and N without one.
func TestPackScriptReport(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	core := filepath.Join(dir, "core")

	require.NoError(t, os.WriteFile(core, []byte("the core"), 0o600))

	tarantool := fakeTarantool(t, dir)

	for _, pid := range []string{"N", "4242"} {
		out := t.TempDir()
		report := filepath.Join(t.TempDir(), "archive")
		args := []string{"-c", core, "-e", tarantool, "-d", out, "-t", "1763869440", "-r", report}

		if pid != "N" {
			args = append(args, "-p", pid)
		}

		output, err := runScript(t, "not the core", args...)
		require.NoError(t, err, output)
		assert.NotContains(t, output, "The resulting archive is located here", pid)

		archives, err := filepath.Glob(filepath.Join(out, "*.tar.gz"))
		require.NoError(t, err)
		require.Len(t, archives, 1, pid)
		assert.True(t, strings.HasPrefix(filepath.Base(archives[0]), "tarantool-core-"+pid+"-"),
			archives[0])

		content, err := os.ReadFile(report)
		require.NoError(t, err, pid)
		assert.Equal(t, archives[0]+"\n", string(content), pid)
	}
}

// archiveDirs returns the top directories of the members of the tar.gz
// archive at path.
func archiveDirs(t *testing.T, path string) []string {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	compressed, err := gzip.NewReader(file)
	require.NoError(t, err)

	var dirs []string

	reader := tar.NewReader(compressed)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		dir, _, _ := strings.Cut(header.Name, "/")
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// TestPackScriptTakenName runs the pack script, as Pack does, on the same
// core of the same time and PID three times: the archive of the name there
// already stays as it is, and the next packs take the name with -2 and -3
// before .tar.gz, for the archive and its directory alike, and report the
// archive of the first name on the second line of -r. The second pack
// replaced the archive: the name has the minute of the time alone.
func TestPackScriptTakenName(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	out := t.TempDir()
	core := filepath.Join(dir, "core")

	require.NoError(t, os.WriteFile(core, []byte("the core"), 0o600))

	args := []string{
		"-c", core, "-e", fakeTarantool(t, dir), "-d", out, "-p", "42", "-t", "1763869440",
	}

	var name string

	var first []byte

	for index, suffix := range []string{"", "-2", "-3"} {
		report := filepath.Join(t.TempDir(), "archive")

		output, err := runScript(t, "", append(args, "-r", report)...)
		require.NoError(t, err, output)

		archives, err := filepath.Glob(filepath.Join(out, "*.tar.gz"))
		require.NoError(t, err)
		require.Len(t, archives, index+1, suffix)

		if name == "" {
			name = strings.TrimSuffix(filepath.Base(archives[0]), ".tar.gz")
			require.True(t, strings.HasPrefix(name, "tarantool-core-42-"), name)
		}

		archive := filepath.Join(out, name+suffix+".tar.gz")

		content, err := os.ReadFile(report)
		require.NoError(t, err, suffix)

		want := archive + "\n"
		if suffix != "" {
			want += filepath.Join(out, name+".tar.gz") + "\n"
		}

		assert.Equal(t, want, string(content), suffix)
		assert.Equal(t, []string{name + suffix}, archiveDirs(t, archive), suffix)
		assert.Equal(t, "the core", archiveMembers(t, archive)["coredump"], suffix)

		packed, err := os.ReadFile(filepath.Join(out, name+".tar.gz"))
		require.NoError(t, err)

		if first == nil {
			first = packed
		}

		assert.Equal(t, first, packed, "the first archive stays as it was")
	}

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Len(t, entries, 3, "the archives alone")
}

// TestPackScriptNameNotRegular runs the pack script where the names of the
// archive are taken by files that are no regular ones: a symbolic link to
// /dev/null, a FIFO, a symbolic link to nothing and a directory. The pack
// takes the first name none of them has, -5, and creates the archive as a
// regular file there, and leaves each as it was; the first name taken is
// reported. The noclobber of the shell opened the link to /dev/null, which
// tar wrote the archive to, and a FIFO, where the pack waited for a reader.
func TestPackScriptNameNotRegular(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	out := t.TempDir()
	core := filepath.Join(dir, "core")
	report := filepath.Join(t.TempDir(), "archive")

	require.NoError(t, os.WriteFile(core, []byte("the core"), 0o600))

	args := []string{
		"-c", core, "-e", fakeTarantool(t, dir), "-d", out, "-p", "42", "-t", "1763869440",
		"-r", report,
	}

	// The name is the one a pack of the same core and time takes.
	probe := t.TempDir()

	output, err := runScript(t, "", "-c", core, "-e", fakeTarantool(t, dir), "-d", probe,
		"-p", "42", "-t", "1763869440")
	require.NoError(t, err, output)

	archives, err := filepath.Glob(filepath.Join(probe, "*.tar.gz"))
	require.NoError(t, err)
	require.Len(t, archives, 1)

	name := strings.TrimSuffix(filepath.Base(archives[0]), ".tar.gz")
	first := filepath.Join(out, name+".tar.gz")

	require.NoError(t, os.Symlink(os.DevNull, first))
	require.NoError(t, syscall.Mkfifo(filepath.Join(out, name+"-2.tar.gz"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(dir, "nothing"),
		filepath.Join(out, name+"-3.tar.gz")))
	require.NoError(t, os.Mkdir(filepath.Join(out, name+"-4.tar.gz"), 0o700))

	output, err = runScript(t, "", args...)
	require.NoError(t, err, output)

	archive := filepath.Join(out, name+"-5.tar.gz")

	content, err := os.ReadFile(report)
	require.NoError(t, err)
	assert.Equal(t, archive+"\n"+first+"\n", string(content))

	info, err := os.Lstat(archive)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), info.Mode())
	assert.Equal(t, "the core", archiveMembers(t, archive)["coredump"])

	target, err := os.Readlink(first)
	require.NoError(t, err)
	assert.Equal(t, os.DevNull, target)

	info, err = os.Lstat(filepath.Join(out, name+"-2.tar.gz"))
	require.NoError(t, err)
	assert.Equal(t, os.ModeNamedPipe, info.Mode().Type())

	entries, err := os.ReadDir(filepath.Join(out, name+"-4.tar.gz"))
	require.NoError(t, err)
	assert.Empty(t, entries)

	entries, err = os.ReadDir(out)
	require.NoError(t, err)
	assert.Len(t, entries, 5, "the four names and the archive alone")
}

// TestPackArchive runs Pack on a core of a tarantool cc built, of the
// process 4242: it returns the path of the archive, which it named by the
// PID of the core without one given, where it named it N, and by the one
// given otherwise, as the pipe gives it. Packed again into the directory,
// the archive is named apart, and Pack returns the one of the name too.
func TestPackArchive(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	tarantool := compileTarantool(t, dir)
	core := binaryCore(t, tarantool, 0x555555554000)

	core.notes = prpsinfoNote(4242)

	opts := PackOptions{
		Core: core.write(t, filepath.Join(dir, "core")), Executable: tarantool, Tarantools: nil,
		Dir: "", PID: 0, Time: "1763869440",
	}

	streams := output.Streams{In: nil, Out: io.Discard, Err: io.Discard}

	for pid, name := range map[uint]string{0: "4242", 7: "7"} {
		opts.Dir, opts.PID = t.TempDir(), pid

		log, _ := logtest.New(t)

		archive, taken, err := Pack(streams, log, opts)
		require.NoError(t, err, pid)
		assert.Empty(t, taken, pid)

		archives, err := filepath.Glob(filepath.Join(opts.Dir, "*.tar.gz"))
		require.NoError(t, err)
		assert.Equal(t, []string{archive}, archives, pid)
		assert.True(t, strings.HasPrefix(filepath.Base(archive), "tarantool-core-"+name+"-"),
			archive)

		again, taken, err := Pack(streams, log, opts)
		require.NoError(t, err, pid)
		assert.Equal(t, archive, taken, pid)
		assert.Equal(t, strings.TrimSuffix(archive, ".tar.gz")+"-2.tar.gz", again, pid)
	}
}

// TestPackScriptPipe runs the pack script on its own with -p of the test, a
// process of procfs whose comm is not tarantool. Without -c the script is
// the kernel.core_pattern pipe: it stores the core of its standard input
// as <comm>-core.<PID>.<TIME> and packs nothing. With -c it packs the core
// given and reads no standard input, as tt runs it, with the core of a pipe
// stored: it took the standard input for the core all the same, which was
// the text of the script fed to bash.
func TestPackScriptPipe(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	comm, err := os.ReadFile("/proc/self/comm")
	require.NoError(t, err)

	pid := strconv.Itoa(os.Getpid())
	stored := strings.TrimSpace(string(comm)) + "-core." + pid + ".1763869440"

	out := t.TempDir()

	_, err = runScript(t, "the core of a pipe", "-d", out, "-p", pid, "-t", "1763869440")
	require.NoError(t, err)

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the core alone")
	assert.Equal(t, stored, entries[0].Name())

	content, err := os.ReadFile(filepath.Join(out, stored))
	require.NoError(t, err)
	assert.Equal(t, "the core of a pipe", string(content))

	dir := t.TempDir()
	core := filepath.Join(dir, "core")

	require.NoError(t, os.WriteFile(core, []byte("the core of a file"), 0o600))

	out = t.TempDir()

	output, err := runScript(t, "not the core", "-c", core, "-e", fakeTarantool(t, dir), "-d",
		out, "-p", pid, "-t", "1763869440")
	require.NoError(t, err, output)

	archives, err := filepath.Glob(filepath.Join(out, "tarantool-core-"+pid+"-*.tar.gz"))
	require.NoError(t, err)
	require.Len(t, archives, 1, "an archive named by the PID")

	entries, err = os.ReadDir(out)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the archive alone")
	assert.Equal(t, "the core of a file", archiveMembers(t, archives[0])["coredump"])
}
