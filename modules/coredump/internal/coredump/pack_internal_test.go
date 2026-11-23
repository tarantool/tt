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
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
// failed pack would take stays.
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
