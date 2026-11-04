package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

func TestComponentInputs(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	comp := filepath.Join(project, "comp")
	writeFile(t, filepath.Join(project, ".gitignore"), "comp/generated/\n")
	writeFile(t, filepath.Join(comp, ".gitignore"), "dist/\n*.pyc\n")
	writeFile(t, filepath.Join(comp, "main.py"), "print(1)\n")
	writeFile(t, filepath.Join(comp, "wit", "app.wit"), "world app {}\n")

	build := manifest.Build{Backend: "shell", Command: "tarawasm", Args: []string{"all"}}
	hash := func() string {
		t.Helper()

		got, err := componentInputs(project, comp, build)
		require.NoError(t, err)

		return got
	}

	first := hash()
	assert.Equal(t, first, hash(), "the same inputs hash the same")

	// What git does not keep is no input: outputs, bindings, hidden state.
	writeFile(t, filepath.Join(comp, "dist", "app.wasm"), "output")
	writeFile(t, filepath.Join(comp, "cache.pyc"), "bytecode")
	writeFile(t, filepath.Join(comp, "generated", "bindings.py"), "ignored by the project")
	writeFile(t, filepath.Join(comp, ".tarawasm", "state"), "hidden")
	assert.Equal(t, first, hash())

	writeFile(t, filepath.Join(comp, "main.py"), "print(2)\n")

	second := hash()
	assert.NotEqual(t, first, second, "a changed source is a changed input")

	build.Args = []string{"build"}

	assert.NotEqual(t, second, hash(), "a changed build block is a changed input")
}

func TestIncrementalRecord(t *testing.T) {
	t.Parallel()

	project := t.TempDir()

	_, found := readIncremental(project, "comp")
	assert.False(t, found)

	record := incrementalRecord{
		Inputs:  "abc",
		Written: []string{"sub/x.so"},
		LaidOut: []string{"init.lua"},
	}
	require.NoError(t, writeIncremental(project, "comp", record))

	got, found := readIncremental(project, "comp")
	assert.True(t, found)
	assert.Equal(t, record, got)
}

func TestWrittenFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "kept.so"), "kept")
	writeFile(t, filepath.Join(dir, "other.so"), "another component's")

	before, err := stampTree(dir)
	require.NoError(t, err)

	writeFile(t, filepath.Join(dir, "sub", "new.so"), "new")

	after, err := stampTree(dir)
	require.NoError(t, err)

	assert.Equal(t, []string{"kept.so", "sub/new.so"},
		writtenFiles(before, after, []string{"kept.so", "gone.so"}),
		"what the last build wrote stays the build's while it is there")

	missing, err := stampTree(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.Empty(t, missing)
}

// TestRunSkipsUnchangedIncrementalComponent builds a project twice and then
// once more after a change: the shell build of the incremental component
// runs the first time and after the change only, and the file it makes is
// laid out every time.
func TestRunSkipsUnchangedIncrementalComponent(t *testing.T) {
	t.Parallel()

	const incrementalManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.comp]
path = 'comp'
include = ['out.txt']

[components.comp.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo ran >> ../runs.log && cp in.txt out.txt']
incremental = true
`

	dir := setupProject(t, incrementalManifest, map[string]string{
		"comp/in.txt":     "one",
		"comp/.gitignore": "out.txt\n",
	})
	runs := func() int {
		t.Helper()

		data, err := os.ReadFile(filepath.Join(dir, "runs.log"))
		require.NoError(t, err)

		return strings.Count(string(data), "ran")
	}
	laidOut := filepath.Join(dir, ".rocks", "share", "tarantool", "my-app", "out.txt")

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, runs())

	require.NoError(t, os.RemoveAll(filepath.Join(dir, ".rocks")))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, runs(), "nothing changed: the build is skipped")
	assert.FileExists(t, laidOut, "a skipped build is laid out all the same")

	writeFile(t, filepath.Join(dir, "comp", "in.txt"), "two")
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 2, runs(), "a changed input builds again")

	data, err := os.ReadFile(laidOut)
	require.NoError(t, err)
	assert.Equal(t, "two", string(data))

	// An output that is gone is built again too.
	require.NoError(t, os.Remove(filepath.Join(dir, "comp", "out.txt")))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 3, runs())

	// Rebuild builds what did not change.
	rebuild := dryOptions(dir)

	rebuild.Rebuild = true

	require.NoError(t, Run(context.Background(), rebuild))
	assert.Equal(t, 4, runs())

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 4, runs(), "a rebuild is recorded as any build")
}

// incrementalShell is my-app with an incremental module component, comp,
// built by a shell command; extra is more of its build block.
func incrementalShell(command, extra string) string {
	return `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.comp]
path = 'comp'

[components.comp.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''echo ran >> ../runs.log && ` + command + `''']
incremental = true
` + extra
}

// countRuns counts the builds the shell command of a test ran.
func countRuns(t *testing.T, dir string) int {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "runs.log"))
	require.NoError(t, err)

	return strings.Count(string(data), "ran")
}

// TestIncrementalWritesIntoOutputDir builds a component that writes into
// TT_OUTPUT_DIR, as a make target does, and has a source the layout takes:
// a build is skipped only while what it wrote is still in the tree.
func TestIncrementalWritesIntoOutputDir(t *testing.T) {
	t.Parallel()

	// native.so is written only when it is missing, as by a make target
	// that is up to date; sub/fast.so every time.
	dir := setupProject(t, incrementalShell(`mkdir -p "$TT_OUTPUT_DIR/sub" && `+
		`{ [ -f "$TT_OUTPUT_DIR/native.so" ] || cp lib.txt "$TT_OUTPUT_DIR/native.so"; } && `+
		`cp lib.txt "$TT_OUTPUT_DIR/sub/fast.so"`, ""), map[string]string{
		"comp/lib.txt":  "lib",
		"comp/init.lua": "return {}",
	})
	lib := filepath.Join(dir, ".rocks", "lib", "tarantool", "my-app")
	build := func(opts Options) {
		t.Helper()
		require.NoError(t, Run(context.Background(), opts))
		assert.FileExists(t, filepath.Join(lib, "native.so"))
		assert.FileExists(t, filepath.Join(lib, "sub", "fast.so"))
	}

	build(dryOptions(dir))
	assert.Equal(t, 1, countRuns(t, dir))

	build(dryOptions(dir))
	assert.Equal(t, 1, countRuns(t, dir), "nothing changed: the build is skipped")

	require.NoError(t, os.RemoveAll(filepath.Join(dir, ".rocks")))
	build(dryOptions(dir))
	assert.Equal(t, 2, countRuns(t, dir), "what the build wrote is gone: it builds again")

	// A rebuild that leaves native.so alone keeps it the build's.
	rebuild := dryOptions(dir)

	rebuild.Rebuild = true

	build(rebuild)
	assert.Equal(t, 3, countRuns(t, dir))

	require.NoError(t, os.Remove(filepath.Join(lib, "native.so")))
	build(dryOptions(dir))
	assert.Equal(t, 4, countRuns(t, dir))
}

// TestIncrementalCopiesOutput builds a component whose output is listed: a
// skipped build copies it into the tree again, and builds nothing.
func TestIncrementalCopiesOutput(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, incrementalShell("cp in.txt out.bin", "output = ['out.bin']\n"),
		map[string]string{
			"comp/in.txt":     "one",
			"comp/.gitignore": "out.bin\n",
		})
	copied := filepath.Join(dir, ".rocks", "lib", "tarantool", "my-app", "out.bin")

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, countRuns(t, dir))

	require.NoError(t, os.RemoveAll(filepath.Join(dir, ".rocks")))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, countRuns(t, dir), "the output is there to copy: the build is skipped")
	assert.FileExists(t, copied)

	require.NoError(t, os.Remove(filepath.Join(dir, "comp", "out.bin")))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 2, countRuns(t, dir))
	assert.FileExists(t, copied)
}

// TestIncrementalExecutable builds an incremental executable component: a
// skipped build keeps its program in the record of programs, and a program
// that is gone builds it again.
func TestIncrementalExecutable(t *testing.T) {
	t.Parallel()

	manifest := manifestHead + `
[components.tools]
path = 'tools'
kind = 'executable'

[components.tools.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''echo ran >> ../runs.log && cp ` + hostProgram(t) + ` "$TT_OUTPUT_DIR/prog"''']
incremental = true
`
	dir := setupProject(t, manifest, map[string]string{
		"init.lua":     "return {}",
		"tools/README": "the tools of my-app",
	})
	program := filepath.Join(dir, ".rocks", "bin", "prog")

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, countRuns(t, dir))

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, countRuns(t, dir), "nothing changed: the build is skipped")
	assert.Equal(t, map[string][]string{"tools": {"prog"}}, readRecord(t, dir))

	require.NoError(t, os.Remove(program))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 2, countRuns(t, dir), "the program is gone: it builds again")
	assert.FileExists(t, program)
}

func TestIncrementalIsForMakeAndShell(t *testing.T) {
	t.Parallel()

	const goManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.prog]
path = 'prog'
kind = 'executable'

[components.prog.build]
backend = 'go'
incremental = true
`

	man, _, err := manifest.ParseManifest([]byte(goManifest))
	require.NoError(t, err)

	_, err = man.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incremental")
}
