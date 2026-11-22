package coredump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/output"
)

// fakeGDB is a gdb that answers the question of gdb.sh about the line of
// main as a gdb on a tarantool with debugging information does, and
// otherwise prints its arguments, one per line, after a line "gdb".
const fakeGDB = `#!/bin/sh
if [ "$1" = -batch ]; then
	echo 'Line 747 of "/build/tarantool/src/main.cc" starts at address 0x1 <main>.'
	exit 0
fi
echo gdb
for arg in "$@"; do
	echo "$arg"
done
`

// withFakeGDB puts fakeGDB first in PATH for the test. The wrapper needs
// GNU getopt and grep -P: the test runs on Linux only.
func withFakeGDB(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("gdb.sh needs GNU getopt and grep")
	}

	bin := t.TempDir()
	require.NoError(t, execfile.WriteFile(filepath.Join(bin, "gdb"), []byte(fakeGDB), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// artefacts are the files of an unpacked archive, by name: its gdb.sh
// fails, so that a run of it shows, and its extension is not tt's.
var artefacts = map[string]string{
	"version":        "Tarantool 3.9.0-0-g8b9a0f59a95b\n",
	"tarantool":      "ELF",
	"coredump":       "ELF",
	"gdb.sh":         "#!/bin/sh\necho the gdb.sh of the archive\nexit 99\n",
	"ext/old-gdb.py": "print('the extension of the archive')\n",
}

// writeArtefacts writes artefacts into dir.
func writeArtefacts(t *testing.T, dir string) {
	t.Helper()

	for name, content := range artefacts {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, execfile.WriteFile(path, []byte(content), 0o755))
	}
}

// writeArtefactsArchive writes artefacts into an archive in dir, under the
// directory the name of the archive names, as pack does.
func writeArtefactsArchive(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "tarantool-core-N-202611230344-host.tar.gz")

	var buffer bytes.Buffer

	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)

	for name, content := range artefacts {
		require.NoError(t, writer.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: "tarantool-core-N-202611230344-host/" + name,
			Mode: 0o755, Size: int64(len(content)),
		}))

		_, err := writer.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, os.WriteFile(path, buffer.Bytes(), 0o644))

	return path
}

// inspect runs Inspect and returns what it printed to stdout.
func inspect(t *testing.T, archiveOrDir, sourceDir string) string {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := Inspect(output.Streams{In: strings.NewReader(""), Out: &stdout, Err: &stderr},
		archiveOrDir, sourceDir)
	require.NoError(t, err, stdout.String()+stderr.String())

	return stdout.String()
}

// sourced returns the files the gdb command line of out sources.
func sourced(out string) []string {
	var files []string

	for line := range strings.Lines(out) {
		file, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "source ")
		if ok {
			files = append(files, filepath.Base(file))
		}
	}

	return files
}

func TestInspectArgs(t *testing.T) {
	t.Parallel()

	args, err := inspectArgs("/work/gdb.sh", "/dumps/core", "", []string{"/work/a.py"})
	require.NoError(t, err)
	assert.Equal(t, []string{"/work/gdb.sh", "-d", "/dumps/core", "-x", "/work/a.py"}, args)

	args, err = inspectArgs("/work/gdb.sh", "/dumps/core", "/src/tarantool",
		[]string{"/work/a.py", "/work/b.py"})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"/work/gdb.sh", "-d", "/dumps/core", "-s", "/src/tarantool",
		"-x", "/work/a.py", "-x", "/work/b.py",
	}, args)
}

// TestInspectDirectory checks that inspect of an unpacked archive runs the
// wrapper of tt rather than the archive's, on the artefacts of the
// directory, and that the wrapper sources the extensions of tt once the
// core is loaded, gdb.printing imported first.
//
//nolint:paralleltest // Sets PATH.
func TestInspectDirectory(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)

	out := inspect(t, dir, "")
	assert.NotContains(t, out, "the gdb.sh of the archive")
	assert.Contains(t, out, "gdb\n"+filepath.Join(dir, "tarantool")+"\n")
	assert.Contains(t, out, "-ex\nset sysroot "+dir+"\n")
	assert.Contains(t, out, "-ex\ncore "+filepath.Join(dir, "coredump")+"\n")

	embedded, err := extensions.ReadDir(extensionsDir)
	require.NoError(t, err)

	names := make([]string, 0, len(embedded))
	for _, entry := range embedded {
		names = append(names, entry.Name())
	}

	assert.Equal(t, names, sourced(out))

	core := strings.Index(out, "core "+filepath.Join(dir, "coredump"))
	printing := strings.Index(out, "python import gdb.printing")
	source := strings.Index(out, "source ")

	assert.Less(t, core, printing, "gdb.printing is imported once the core is loaded")
	assert.Less(t, printing, source, "gdb.printing is imported before the extensions")

	// The build path of the line of main maps to the sources, the
	// directory sources of the artefacts unless -s names another.
	assert.Contains(t, out, "set substitute-path /build/tarantool "+
		filepath.Join(dir, "sources")+"\n")

	sources := t.TempDir()

	out = inspect(t, dir, sources)
	assert.Contains(t, out, "set substitute-path /build/tarantool "+sources+"\n")
}

// TestInspectArchive checks that inspect of an archive does the same on
// the artefacts it unpacks.
//
//nolint:paralleltest // Sets PATH.
func TestInspectArchive(t *testing.T) {
	withFakeGDB(t)

	archive := writeArtefactsArchive(t, t.TempDir())

	out := inspect(t, archive, "")
	assert.NotContains(t, out, "the gdb.sh of the archive")
	assert.Regexp(t, `\n-ex\ncore /.*/tarantool-core-N-202611230344-host/coredump\n`, out)
	assert.NotContains(t, sourced(out), "old-gdb.py")
	assert.NotEmpty(t, sourced(out))
}

// TestWrapperSourcesTheArchiveExtensions checks that the gdb.sh of an
// archive, run by hand, sources the extensions in ext/ next to it.
//
//nolint:paralleltest // Sets PATH.
func TestWrapperSourcesTheArchiveExtensions(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)
	require.NoError(t, copyEmbedded(coreScripts, inspectEmbedPath, filepath.Join(dir, "gdb.sh"),
		scriptFileMode))

	out, err := exec.CommandContext(context.Background(), "sh",
		filepath.Join(dir, "gdb.sh")).CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "-ex\npython import gdb.printing\n-ex\nsource "+
		filepath.Join(dir, "ext", "old-gdb.py")+"\n")
}

// TestWrapperWithoutVersion checks that the wrapper refuses a directory
// that is not an unpacked archive.
//
//nolint:paralleltest // Sets PATH.
func TestWrapperWithoutVersion(t *testing.T) {
	withFakeGDB(t)

	var stdout bytes.Buffer

	err := Inspect(output.Streams{In: strings.NewReader(""), Out: &stdout, Err: &stdout},
		t.TempDir(), "")
	require.ErrorContains(t, err, "inspect script execution failed")
	assert.Contains(t, stdout.String(), "version file is missing")
}
