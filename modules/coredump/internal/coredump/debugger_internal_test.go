package coredump

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// recordingDebugger is a debugger of any name whose command does nothing:
// it records what it is handed, the extensions by name, in order, with
// their content. Its probe is probe, or fails with probeErr; it tells a
// debugger 17.1 with Python, which nothing is warned of, unless set.
type recordingDebugger struct {
	name      string
	probe     debuggerProbe
	probeErr  error
	dir       string
	sourceDir string
	exts      []string
	contents  map[string]string
}

// newRecordingDebugger returns a recordingDebugger named name.
func newRecordingDebugger(name string) *recordingDebugger {
	return &recordingDebugger{
		name: name,
		probe: debuggerProbe{
			Path: "/usr/bin/" + name, Version: debuggerVersion{17, 1}, Python: true,
		},
		contents: map[string]string{},
	}
}

// Name implements debugger.
func (d *recordingDebugger) Name() string {
	return d.name
}

// Probe implements debugger.
func (d *recordingDebugger) Probe(context.Context) (debuggerProbe, error) {
	return d.probe, d.probeErr
}

// Command implements debugger: it reads the extensions, which exist until
// the command ends, and returns a command that exits 0.
func (d *recordingDebugger) Command(ctx context.Context, _, dir, sourceDir string,
	exts []string,
) (*exec.Cmd, error) {
	d.dir, d.sourceDir = dir, sourceDir

	for _, path := range exts {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		d.exts = append(d.exts, filepath.Base(path))
		d.contents[filepath.Base(path)] = string(data)
	}

	return exec.CommandContext(ctx, "sh", "-c", "exit 0"), nil
}

// TestInspectInDebugger checks that a debugger is handed the extensions
// that load in it, in their order, wherever they come from: lldb only
// luajit_dbg.py, gdb both, the one of LuaJIT first.
func TestInspectInDebugger(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeArtefacts(t, dir)

	run := func(t *testing.T, backend debugger, sourceDir string, status int) {
		t.Helper()

		github, server := newFakeGitHub(t)
		if status != 0 {
			github.fail(status)
		}

		res, _ := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err := inspectWith(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, backend, InspectOptions{Path: dir, SourceDir: sourceDir})
		require.NoError(t, err, stdout.String())
	}

	// Of GitHub, by the table of the upstream extensions.
	lldb := newRecordingDebugger(debuggerLLDB)
	run(t, lldb, "", 0)
	assert.Equal(t, []string{luajitDbgName}, lldb.exts)
	assert.Equal(t, "luajit_dbg.py of "+luajitNew, lldb.contents[luajitDbgName])
	assert.Equal(t, dir, lldb.dir)

	gdb := newRecordingDebugger(debuggerGDB)
	run(t, gdb, "", 0)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, gdb.exts)

	// The copies of tt, by their manifest.
	lldb = newRecordingDebugger(debuggerLLDB)
	run(t, lldb, "", http.StatusBadGateway)
	assert.Equal(t, []string{luajitDbgName}, lldb.exts)
	assert.Equal(t, embeddedContent(t, luajitDbgName), lldb.contents[luajitDbgName])

	gdb = newRecordingDebugger(debuggerGDB)
	run(t, gdb, "", http.StatusBadGateway)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, gdb.exts)

	// Of the sources: luajit-gdb.py, the older extension of LuaJIT, loads
	// in gdb only.
	sources := writeSources(t, map[string]string{
		"third_party/luajit/src/luajit-gdb.py": "luajit-gdb.py of the sources",
		"tools/tarantool-gdb.py":               "tarantool-gdb.py of the sources",
	})

	lldb = newRecordingDebugger(debuggerLLDB)
	run(t, lldb, sources, 0)
	assert.Empty(t, lldb.exts)
	assert.Equal(t, sources, lldb.sourceDir)

	gdb = newRecordingDebugger(debuggerGDB)
	run(t, gdb, sources, 0)
	assert.Equal(t, []string{luajitGDBName, tarantoolGDBName}, gdb.exts)
	assert.Equal(t, "luajit-gdb.py of the sources", gdb.contents[luajitGDBName])
}

// TestInspectGDBToolsInDebugger checks that with GDBTools a debugger is
// handed the universal extensions of tarantool-gdb-tools that load in it,
// each after those its manifest names in after: gdb all four, lj-bt after
// the extension of LuaJIT, whose helpers it takes, and lldb that one
// alone. Neither GitHub nor the extensions of the sources are looked at,
// the sources are still the debugger's to map, and the log names the
// extensions and their commit. Without GDBTools the extensions are those
// of the Tarantool of the core, of the sources here.
func TestInspectGDBToolsInDebugger(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeArtefacts(t, dir)

	sources := writeSources(t, map[string]string{
		"third_party/luajit/src/luajit_dbg.py": "luajit_dbg.py of the sources",
		"tools/tarantool-gdb.py":               "tarantool-gdb.py of the sources",
	})

	run := func(t *testing.T, backend debugger, gdbTools bool,
	) (*fakeGitHub, *logtest.Recorder) {
		t.Helper()

		github, server := newFakeGitHub(t)
		res, recorder := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err := inspectWith(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, backend, InspectOptions{Path: dir, SourceDir: sources, GDBTools: gdbTools})
		require.NoError(t, err, stdout.String())

		return github, recorder
	}

	gdb := newRecordingDebugger(debuggerGDB)
	github, recorder := run(t, gdb, true)
	assert.ElementsMatch(t,
		[]string{gdbToolsLuaJIT, gdbToolsTarantool, gdbToolsBacktrace, gdbToolsGcore}, gdb.exts)
	assert.Less(t, slices.Index(gdb.exts, gdbToolsLuaJIT), slices.Index(gdb.exts,
		gdbToolsBacktrace), "lj-bt is loaded after the extension of LuaJIT")

	for _, name := range gdb.exts {
		assert.Equal(t, gdbToolsContent(t, name), gdb.contents[name], name)
	}

	assert.Equal(t, sources, gdb.sourceDir)
	assert.Empty(t, github.Requests())
	assert.Empty(t, warnings(recorder))
	assert.Contains(t, recorder.Messages(), "gdb extensions of tarantool-gdb-tools "+
		GDBToolsCommit()+", which tt embeds: "+strings.Join(gdb.exts, ", "))

	lldb := newRecordingDebugger(debuggerLLDB)

	github, recorder = run(t, lldb, true)
	assert.Equal(t, []string{gdbToolsLuaJIT}, lldb.exts)
	assert.Equal(t, gdbToolsContent(t, gdbToolsLuaJIT), lldb.contents[gdbToolsLuaJIT])
	assert.Empty(t, github.Requests())
	assert.Empty(t, warnings(recorder))

	gdb = newRecordingDebugger(debuggerGDB)
	run(t, gdb, false)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, gdb.exts)
	assert.Equal(t, "luajit_dbg.py of the sources", gdb.contents[luajitDbgName])
}

// TestResolveDebuggers checks that every extension resolve returns says the
// debuggers it loads in: one of GitHub or of the sources by the table of the
// upstream extensions, a copy of tt by its manifest.
func TestResolveDebuggers(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)

	res, _ := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.5.0-0-g0000000")
	require.NoError(t, err)
	require.Equal(t, []string{luajitGDBName, tarantoolGDBName}, names(exts))
	assert.Equal(t, []string{debuggerGDB}, exts[0].Debuggers)
	assert.Equal(t, []string{debuggerGDB}, exts[1].Debuggers)

	github.fail(http.StatusForbidden)

	res, _ = testResolver(t, server, t.TempDir())

	exts, err = res.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)

	entries, err := embeddedManifest()
	require.NoError(t, err)
	require.Len(t, exts, len(entries))

	for i, entry := range entries {
		assert.Equal(t, entry.File, exts[i].Name)
		assert.Equal(t, entry.Debuggers, exts[i].Debuggers, entry.File)
		assert.Equal(t, entry.After, exts[i].After, entry.File)
	}
}

// TestUpstreamDebuggers checks the debuggers of the upstream extensions,
// and that the table agrees with the manifest on the files tt has copies
// of.
func TestUpstreamDebuggers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, map[string][]string{
		luajitDbgName:    {debuggerGDB, debuggerLLDB},
		luajitGDBName:    {debuggerGDB},
		tarantoolGDBName: {debuggerGDB},
	}, upstreamDebuggers)

	entries, err := embeddedManifest()
	require.NoError(t, err)

	for _, entry := range entries {
		ext := upstreamExtension(entry.File, nil)
		assert.Equal(t, entry.Debuggers, ext.Debuggers, entry.File)
		assert.Empty(t, entry.After, entry.File)
	}
}

// TestForDebugger checks that the extensions for a debugger are those that
// load in it, each after those its After names and otherwise in the order
// given, and that one is left out, with what it misses, when one its After
// names is not loaded: the extension needs it.
func TestForDebugger(t *testing.T) {
	t.Parallel()

	exts := []extension{
		{Name: "b.py", Debuggers: []string{debuggerGDB, debuggerLLDB}, After: []string{"a.py"}},
		{Name: "c.py", Debuggers: []string{debuggerGDB}},
		{Name: "a.py", Debuggers: []string{debuggerGDB, debuggerLLDB}, After: []string{"c.py"}},
		{Name: "d.py", Debuggers: nil},
		{Name: "e.py", Debuggers: []string{debuggerGDB, debuggerLLDB}},
	}

	got, skipped, err := forDebugger(exts, debuggerGDB)
	require.NoError(t, err)
	assert.Equal(t, []string{"c.py", "a.py", "b.py", "e.py"}, names(got))
	assert.Empty(t, skipped)

	// c.py does not load in lldb: neither does a.py, which needs it, nor
	// b.py, which needs a.py.
	got, skipped, err = forDebugger(exts, debuggerLLDB)
	require.NoError(t, err)
	assert.Equal(t, []string{"e.py"}, names(got))
	assert.ElementsMatch(t, []skippedExtension{
		{Name: "a.py", Missing: "c.py"},
		{Name: "b.py", Missing: "a.py"},
	}, skipped)

	// One that is not among the extensions at all is missed too.
	got, skipped, err = forDebugger([]extension{
		{Name: "a.py", Debuggers: []string{debuggerGDB}, After: []string{"z.py"}},
	}, debuggerGDB)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, []skippedExtension{{Name: "a.py", Missing: "z.py"}}, skipped)

	got, skipped, err = forDebugger(exts, "windbg")
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, skipped)

	_, _, err = forDebugger([]extension{
		{Name: "a.py", Debuggers: []string{debuggerGDB}, After: []string{"b.py"}},
		{Name: "b.py", Debuggers: []string{debuggerGDB}, After: []string{"a.py"}},
	}, debuggerGDB)
	require.ErrorIs(t, err, errLoadCycle)
	assert.ErrorContains(t, err, "cannot order the extensions for gdb")
}

// TestWarnSkipped checks the warning of an extension inspect does not hand
// the debugger because one it needs does not load there.
func TestWarnSkipped(t *testing.T) {
	t.Parallel()

	log, recorder := logtest.New(t)

	warnSkipped(log, debuggerLLDB, []skippedExtension{
		{Name: "needs.py", Missing: tarantoolGDBName},
	})
	assert.Equal(t, []string{"the extension needs.py is not loaded in lldb: it needs " +
		"tarantool-gdb.py, which is not loaded there"}, recorder.Messages())
}

// TestGDBDebuggerCommand checks that the command of gdb is the gdb.sh of tt,
// written into the directory of inspect, run by sh on the artefacts with
// the extensions given.
func TestGDBDebuggerCommand(t *testing.T) {
	t.Parallel()

	work := t.TempDir()

	backend := gdbDebugger{}
	assert.Equal(t, debuggerGDB, backend.Name())

	cmd, err := backend.Command(t.Context(), work, "/dumps/core", "/src/tarantool",
		[]string{"/work/a.py", "/work/b.py"})
	require.NoError(t, err)

	script := filepath.Join(work, "gdb.sh")
	assert.Equal(t, []string{
		"sh", script, "-d", "/dumps/core", "-s", "/src/tarantool",
		"-x", "/work/a.py", "-x", "/work/b.py",
	}, cmd.Args)

	data, err := os.ReadFile(script)
	require.NoError(t, err)

	embedded, err := coreScripts.ReadFile(inspectEmbedPath)
	require.NoError(t, err)
	assert.Equal(t, string(embedded), string(data))
}
