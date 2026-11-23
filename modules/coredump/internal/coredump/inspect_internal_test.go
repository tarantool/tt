package coredump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// gdbWithPython is how the fake gdb answers the probe of inspect by
// default, as gdb 17.1 with Python does: the first line of show version,
// and the line of its Python.
const gdbWithPython = "echo 'GNU gdb (Ubuntu 17.1-2ubuntu1) 17.1'; echo 'tt-gdb-python: 17.1'"

// fakeGDBScript returns a gdb that answers the probe of inspect with the
// shell commands probe, and the question of gdb.sh about the line of main
// as a gdb on a tarantool with debugging information does. Otherwise it
// prints its arguments, one per line, after a line "gdb", and the first
// line of each file it sources after "content: ".
func fakeGDBScript(probe string) string {
	return `#!/bin/sh
if [ "$1" = -batch ]; then
	case "$*" in
		*"show version"*)
			` + probe + `;;
		*"show debug-file-directory"*)
			echo 'The directory where separate debug symbols are searched for is' \
				'"/usr/lib/debug".';;
		*)
			echo 'Line 747 of "/build/tarantool/src/main.cc" starts at address 0x1 <main>.';;
	esac
	exit 0
fi
echo gdb
for arg in "$@"; do
	echo "$arg"
	case "$arg" in
		"source "*) echo "content: $(head -n 1 "${arg#source }")";;
	esac
done
`
}

// withFakeGDB puts the fake gdb of fakeGDBScript, which answers the probe
// as gdbWithPython, first in PATH for the test. The wrapper needs GNU
// getopt and grep -P: the test runs on Linux only.
func withFakeGDB(t *testing.T) {
	t.Helper()

	withFakeGDBProbe(t, gdbWithPython)
}

// withFakeGDBProbe is withFakeGDB with a gdb that answers the probe with
// the shell commands probe.
func withFakeGDBProbe(t *testing.T, probe string) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("gdb.sh needs GNU getopt and grep")
	}

	bin := t.TempDir()
	require.NoError(t, execfile.WriteFile(filepath.Join(bin, "gdb"),
		[]byte(fakeGDBScript(probe)), 0o755))
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

// artefactsDir is the directory of the artefacts of the test archives, as
// pack names it and the archive after it.
const artefactsDir = "tarantool-core-N-202611230344-host"

// writeArtefactsArchive writes artefacts into an archive in dir, under the
// directory the name of the archive names, as pack does.
func writeArtefactsArchive(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, artefactsDir+".tar.gz")
	writeFilesArchive(t, path, artefactsDir+"/", artefacts)

	return path
}

// writeFilesArchive writes files, by name, into a tar.gz archive at path,
// each under prefix.
func writeFilesArchive(t *testing.T, path, prefix string, files map[string]string) {
	t.Helper()

	var buffer bytes.Buffer

	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)

	for name, content := range files {
		require.NoError(t, writer.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: prefix + name, Mode: 0o755, Size: int64(len(content)),
		}))

		_, err := writer.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, os.WriteFile(path, buffer.Bytes(), 0o644))
}

// runInspect runs inspect with the extensions of the fake GitHub, which has
// those of the version of artefacts, and returns what it printed to stdout
// and the recorder of its log.
func runInspect(t *testing.T, archiveOrDir, sourceDir string) (string, *logtest.Recorder) {
	t.Helper()

	_, server := newFakeGitHub(t)
	res, recorder := testResolver(t, server, t.TempDir())

	var stdout, stderr bytes.Buffer

	err := inspect(t.Context(), output.Streams{
		In: strings.NewReader(""), Out: &stdout, Err: &stderr,
	}, res, InspectOptions{Path: archiveOrDir, SourceDir: sourceDir})
	require.NoError(t, err, stdout.String()+stderr.String())

	return stdout.String(), recorder
}

// sourced returns the files the gdb command line of out sources, and the
// first line of each.
func sourced(out string) map[string]string {
	files := map[string]string{}
	file := ""

	for line := range strings.Lines(out) {
		line = strings.TrimSuffix(line, "\n")

		path, ok := strings.CutPrefix(line, "source ")
		if ok {
			file = filepath.Base(path)
			files[file] = ""
		}

		content, ok := strings.CutPrefix(line, "content: ")
		if ok && file != "" {
			files[file] = content
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
// directory, and that the wrapper sources the extensions of the version of
// the core once the core is loaded, gdb.printing imported first.
//
//nolint:paralleltest // Sets PATH.
func TestInspectDirectory(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)

	out, recorder := runInspect(t, dir, "")
	assert.NotContains(t, out, "the gdb.sh of the archive")
	assert.Contains(t, out, "gdb\n-iex\necho\n"+filepath.Join(dir, "tarantool")+"\n")
	assert.Contains(t, out, "-ex\nset sysroot "+dir+"\n")
	assert.Contains(t, out, "-ex\ncore "+filepath.Join(dir, "coredump")+"\n")
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}, sourced(out))
	assert.Empty(t, warnings(recorder))

	core := strings.Index(out, "core "+filepath.Join(dir, "coredump"))
	printing := strings.Index(out, "python import gdb.printing")
	source := strings.Index(out, "source ")

	assert.Less(t, core, printing, "gdb.printing is imported once the core is loaded")
	assert.Less(t, printing, source, "gdb.printing is imported before the extensions")

	// The build path of the line of main maps to the sources, the
	// directory sources of the artefacts unless -s names another.
	assert.Contains(t, out, "set substitute-path /build/tarantool "+
		filepath.Join(dir, "sources")+"\n")

	// The extensions of the sources come first.
	sources := writeSources(t, map[string]string{
		"third_party/luajit/src/luajit_dbg.py": "luajit_dbg.py of the sources",
		"tools/tarantool-gdb.py":               "tarantool-gdb.py of the sources",
	})

	out, _ = runInspect(t, dir, sources)
	assert.Contains(t, out, "set substitute-path /build/tarantool "+sources+"\n")
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of the sources",
		tarantoolGDBName: "tarantool-gdb.py of the sources",
	}, sourced(out))
}

// TestInspectThreadDB checks that gdb looks for libthread_db next to the
// thread library of the core in the artefacts first, $pdir under the
// sysroot of the artefacts, which the auto-load safe-path lets it load from,
// and in the directories of the host after, $sdir, and that all of it is
// set before the core is loaded. gdb loads libthread_db once it loads the
// core, and a libthread_db of the host that matches the core, as the one of
// the host that packed it does, was taken before the one of the archive.
//
//nolint:paralleltest // Sets PATH.
func TestInspectThreadDB(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)

	out, _ := runInspect(t, dir, "")

	core := strings.Index(out, "\ncore "+filepath.Join(dir, "coredump")+"\n")
	require.Positive(t, core, out)

	for _, command := range []string{
		"set sysroot " + dir,
		"add-auto-load-safe-path " + dir,
		"set auto-load libthread-db on",
		"set libthread-db-search-path $pdir:$sdir",
	} {
		index := strings.Index(out, "\n-ex\n"+command+"\n")
		require.Positive(t, index, command+" in\n"+out)
		assert.Less(t, index, core, command+" before the core is loaded")
	}
}

// TestInspectDebugFile checks that gdb looks for the separate debugging
// information in usr/lib/debug of the artefacts first, then where it looks
// by default, from before it loads the tarantool, and that inspect warns
// of none before gdb starts.
//
//nolint:paralleltest // Sets PATH.
func TestInspectDebugFile(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)
	writeELF(t, filepath.Join(dir, "tarantool"), []testSection{text, buildIDSection(t)}, nil)

	out, recorder := runInspect(t, dir, "")
	assert.Contains(t, out, "gdb\n-iex\necho\n"+filepath.Join(dir, "tarantool")+"\n")
	require.Len(t, warnings(recorder), 2)
	assert.Contains(t, warnings(recorder)[0], "the tarantool of the core has no debugging "+
		"information")
	// The tarantool of the static deb of the version has it.
	assert.Contains(t, warnings(recorder)[1], "hint: the tarantool of the static deb of "+
		"Tarantool 3.9.0, which `tt binary install tarantool 3.9.0` installs, has the debugging "+
		"information: if its build ID is that of the core, "+testBuildID+", copy it over "+
		filepath.Join(dir, "tarantool")+", or to the debug file named above under "+dir+
		", and run `tt coredump inspect "+dir+"`")

	debugFile := filepath.Join(dir, "usr", "lib", "debug", ".build-id", testBuildID[:2],
		testBuildID[2:]+".debug")
	writeELF(t, debugFile, []testSection{strippedText, debugInfo}, nil)

	out, recorder = runInspect(t, dir, "")
	assert.Contains(t, out, "gdb\n-iex\nset debug-file-directory "+
		filepath.Join(dir, "usr", "lib", "debug")+":/usr/lib/debug\n"+
		filepath.Join(dir, "tarantool")+"\n")
	assert.Empty(t, warnings(recorder))
}

// TestInspectArchive checks that inspect of an archive does the same on
// the artefacts it unpacks.
//
//nolint:paralleltest // Sets PATH.
func TestInspectArchive(t *testing.T) {
	withFakeGDB(t)

	archive := writeArtefactsArchive(t, t.TempDir())

	out, _ := runInspect(t, archive, "")
	assert.NotContains(t, out, "the gdb.sh of the archive")
	assert.Regexp(t, `\n-ex\ncore /.*/tarantool-core-N-202611230344-host/coredump\n`, out)
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}, sourced(out))
}

// TestInspectRenamedArchive checks that inspect of an archive opens the
// artefacts of the directory it holds, whatever the archive is named: the
// one pack named, which an archive renamed keeps, or the archive itself of
// one of the artefacts at its root. The checks before gdb read them, and the
// hint of the tarantool of the static deb names the directory tt coredump
// unpack puts them in. inspect took the directory of the name of the
// archive, where gdb.sh found no version and failed.
//
//nolint:paralleltest // Sets PATH.
func TestInspectRenamedArchive(t *testing.T) {
	withFakeGDB(t)

	// A tarantool of a static build without debugging information.
	tarantool := filepath.Join(t.TempDir(), "tarantool")
	writeELF(t, tarantool, []testSection{text, buildIDSection(t)}, nil)

	data, err := os.ReadFile(tarantool)
	require.NoError(t, err)

	files := maps.Clone(artefacts)

	files["tarantool"] = string(data)

	extensions := map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}
	hint := func(archive, unpacked string) string {
		return "hint: the tarantool of the static deb of Tarantool 3.9.0, which `tt binary " +
			"install tarantool 3.9.0` installs, has the debugging information: if its build ID " +
			"is that of the core, " + testBuildID + ", unpack the archive with `tt coredump " +
			"unpack " + archive + "`, copy it over " + filepath.Join(unpacked, "tarantool") +
			", or to the debug file named above under " + unpacked + ", and run `tt coredump " +
			"inspect " + unpacked + "`"
	}

	renamed := filepath.Join(t.TempDir(), "core.tar.gz")
	writeFilesArchive(t, renamed, artefactsDir+"/", files)

	out, recorder := runInspect(t, renamed, "")
	assert.NotContains(t, out, "the gdb.sh of the archive")
	assert.Regexp(t, `\n-ex\ncore /.*/tt-coredump-\d+/`+artefactsDir+`/coredump\n`, out)
	assert.Equal(t, extensions, sourced(out))
	require.Len(t, warnings(recorder), 2)
	assert.Equal(t, hint(renamed, artefactsDir), warnings(recorder)[1])

	root := filepath.Join(t.TempDir(), "root.tar.gz")
	writeFilesArchive(t, root, "", files)

	out, recorder = runInspect(t, root, "")
	assert.Regexp(t, `\n-ex\ncore /.*/tt-coredump-\d+/coredump\n`, out)
	assert.Equal(t, extensions, sourced(out))
	require.Len(t, warnings(recorder), 2)
	assert.Equal(t, hint(root, "."), warnings(recorder)[1])
}

// TestInspectOffline checks that inspect with no GitHub loads the copies
// of the extensions tt embeds, and warns.
//
//nolint:paralleltest // Sets PATH.
func TestInspectOffline(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)

	github, server := newFakeGitHub(t)
	github.fail(http.StatusBadGateway)

	res, recorder := testResolver(t, server, t.TempDir())

	var stdout bytes.Buffer

	err := inspect(t.Context(), output.Streams{
		In: strings.NewReader(""), Out: &stdout, Err: &stdout,
	}, res, InspectOptions{Path: dir})
	require.NoError(t, err, stdout.String())

	firstLine := func(name string) string {
		line, _, _ := strings.Cut(embeddedContent(t, name), "\n")

		return line
	}

	assert.Equal(t, map[string]string{
		luajitDbgName:    firstLine(luajitDbgName),
		tarantoolGDBName: firstLine(tarantoolGDBName),
	}, sourced(stdout.String()))
	assert.Len(t, warnings(recorder), 2)
}

// sourcedOrder returns the names of the files the gdb command line of out
// sources, in the order it sources them.
func sourcedOrder(out string) []string {
	var files []string

	for line := range strings.Lines(out) {
		path, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "source ")
		if ok {
			files = append(files, filepath.Base(path))
		}
	}

	return files
}

// TestInspectGDBTools checks that inspect with GDBTools has gdb.sh source
// the universal extensions of tarantool-gdb-tools tt embeds, in the order of
// their manifest, once the core is loaded, and that -s maps the build path
// to the sources then without their extensions being loaded.
//
//nolint:paralleltest // Sets PATH.
func TestInspectGDBTools(t *testing.T) {
	withFakeGDB(t)

	dir := t.TempDir()
	writeArtefacts(t, dir)

	sources := writeSources(t, map[string]string{
		"third_party/luajit/src/luajit_dbg.py": "luajit_dbg.py of the sources",
		"tools/tarantool-gdb.py":               "tarantool-gdb.py of the sources",
	})

	github, server := newFakeGitHub(t)
	res, _ := testResolver(t, server, t.TempDir())

	var stdout, stderr bytes.Buffer

	err := inspect(t.Context(), output.Streams{
		In: strings.NewReader(""), Out: &stdout, Err: &stderr,
	}, res, InspectOptions{Path: dir, SourceDir: sources, GDBTools: true})
	require.NoError(t, err, stdout.String()+stderr.String())

	out := stdout.String()
	firstLine := func(name string) string {
		line, _, _ := strings.Cut(gdbToolsContent(t, name), "\n")

		return line
	}

	order := []string{gdbToolsLuaJIT, gdbToolsTarantool, gdbToolsBacktrace, gdbToolsGcore}
	assert.Equal(t, order, sourcedOrder(out))

	for name, content := range sourced(out) {
		assert.Equal(t, firstLine(name), content, name)
	}

	assert.Less(t, strings.Index(out, "core "+filepath.Join(dir, "coredump")),
		strings.Index(out, "source "), "the extensions are sourced once the core is loaded")
	assert.Contains(t, out, "set substitute-path /build/tarantool "+sources+"\n")
	assert.Empty(t, github.Requests())
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

	github, server := newFakeGitHub(t)
	res, recorder := testResolver(t, server, t.TempDir())

	var stdout bytes.Buffer

	err := inspect(t.Context(), output.Streams{
		In: strings.NewReader(""), Out: &stdout, Err: &stdout,
	}, res, InspectOptions{Path: t.TempDir()})
	require.ErrorContains(t, err, "inspect script execution failed")
	assert.Contains(t, stdout.String(), "version file is missing")
	assert.Empty(t, github.Requests())
	assert.Empty(t, warnings(recorder))
}
