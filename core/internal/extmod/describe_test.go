package extmod_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/v3/core/internal/extmod"
)

// TestDescribeReplies checks what a module with no manifest is described as
// by its reply to --description --version: a YAML mapping gives its version
// and the first line of its help, and anything else is a help in plain
// text, its first line that is not empty, with no version and no warning.
func TestDescribeReplies(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		reply   string
		version string
		help    string
	}{
		"yaml":           {"version: 2.0.1\nhelp: Says YAML\n", "2.0.1", "Says YAML"},
		"yaml help only": {"help: Says YAML\n", "", "Says YAML"},
		"plain text":     {"\n\n  Says plain text  \nmore\n", "", "Says plain text"},
		"plain colon":    {"usage: plain [args]\n", "", "usage: plain [args]"},
		"plain scalar":   {"1.5\n", "", "1.5"},
		"nothing":        {"", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			main := writeModule(t, dir, "mod", "printf '"+testCase.reply+"'", "")

			tree := newTree(t)

			modules, err := tree.mount(dir)
			require.NoError(t, err)

			_, err = tree.run("modules", "list", "--version")
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("%-5s\tmod - %s\n", testCase.version, testCase.help),
				tree.out.String())
			assert.Empty(t, tree.log.Messages())

			assert.Equal(t, []extmod.Manifest{{
				Name: "mod", Version: testCase.version, Main: main, Help: testCase.help,
			}}, describe(t, modules))
		})
	}
}

// TestDescribeFailingModule checks that a module that fails to describe
// itself is warned about by a listing, once, and by nothing else: not by its
// mount, a completion or another command.
func TestDescribeFailingModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeModule(t, dir, "fails", `echo "broken" >&2; exit 3`, "")
	writeModule(t, dir, "noexec", "", "")
	// An interpreter that does not exist: the module cannot be run at all.
	require.NoError(t, execfile.WriteFile(filepath.Join(dir, "noexec", "main"),
		[]byte("#!/nonexistent/sh\n"), 0o755))

	for _, args := range [][]string{
		{"__complete", ""},
		{"__complete", "f"},
		{"__completeNoDesc", ""},
		{"other"},
	} {
		tree := newTree(t, legacy(internal("other")))

		tree.args = args

		_, err := tree.mount(dir)
		require.NoError(t, err)

		_, err = tree.run(args...)
		require.NoError(t, err, args)
		assert.Empty(t, tree.log.Messages(), args)
	}

	tree := newTree(t)

	modules, err := tree.mount(dir)
	require.NoError(t, err)

	_, err = tree.run("modules", "list")
	require.NoError(t, err)
	assert.Equal(t, "fails - \nnoexec - \n", tree.out.String())

	messages := tree.log.Messages()
	require.Len(t, messages, 2, strings.Join(messages, "\n"))
	assert.Contains(t, messages[0], `Failed to get information about module "fails": `+
		`failed to get module info: exit status 3: broken`)
	assert.Contains(t, messages[1], `Failed to get information about module "noexec": `+
		`failed to get module info:`)

	for _, module := range modules {
		assert.Empty(t, module.Help(), "listed by its name alone")
	}

	assert.Len(t, tree.log.Messages(), 2, "a module is warned about once")
}

// counter writes into dir a module that appends a line to the returned
// file every time it runs, and describes itself as "Counts its runs".
func counter(t *testing.T, dir string) (string, string) {
	t.Helper()

	runs := filepath.Join(t.TempDir(), "runs")
	main := writeModule(t, dir, "counter", `echo run >> "`+runs+`"; `+
		`echo "version: 1.0.0"; echo "help: Counts its runs"`, "")

	return main, runs
}

// countRuns returns how many times the module of counter has run.
func countRuns(t *testing.T, runs string) int {
	t.Helper()

	data, err := os.ReadFile(runs)
	if os.IsNotExist(err) {
		return 0
	}

	require.NoError(t, err)

	return strings.Count(string(data), "run\n")
}

// TestDescribeLazily checks that a module is run to describe itself only
// for a listing - the completion of a command name among them - and not to
// be mounted, nor for another command or another completion.
func TestDescribeLazily(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, runs := counter(t, dir)

	for _, args := range [][]string{
		{"other", "x"},
		{"__complete", "other", ""},
		{"__complete", "-"},
		{"__complete", "x"},
		{"__completeNoDesc", ""},
	} {
		tree := newTree(t, legacy(internal("other")))

		tree.args = args

		_, err := tree.mount(dir)
		require.NoError(t, err)

		_, err = tree.run(args...)
		require.NoError(t, err, args)
		assert.Zero(t, countRuns(t, runs), args)
	}

	tree := newTree(t, legacy(internal("other")))

	tree.args = []string{"__complete", "c"}

	_, err := tree.mount(dir)
	require.NoError(t, err)

	_, err = tree.run(tree.args...)
	require.NoError(t, err)
	assert.Equal(t, 1, countRuns(t, runs), "the completion of a command name asks it")
	assert.Contains(t, tree.out.String(), "counter\tCounts its runs\n")
}

// TestDescribeCache checks that the reply of a module is kept by the path,
// the size and the modification time of its executable: a module that has
// not changed is not asked again, by any listing, and one that has is.
func TestDescribeCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cache := filepath.Join(t.TempDir(), "cache")
	main, runs := counter(t, dir)

	// list mounts the modules on a fresh tree, as a new tt would, and lists
	// them with args.
	list := func(args ...string) string {
		t.Helper()

		tree := newTree(t)

		tree.cacheDir = cache
		tree.args = args

		_, err := tree.mount(dir)
		require.NoError(t, err)

		_, err = tree.run(args...)
		require.NoError(t, err)
		assert.Empty(t, tree.log.Messages())

		return tree.out.String()
	}

	assert.Equal(t, "1.0.0\tcounter - Counts its runs\n", list("modules", "list", "-v"))
	assert.Equal(t, 1, countRuns(t, runs))

	assert.Equal(t, "counter - Counts its runs\n", list("modules", "list"))
	assert.Contains(t, list("__complete", ""), "counter\tCounts its runs\n")
	assert.Equal(t, 1, countRuns(t, runs), "the cache answers")

	// The same executable, touched.
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(main, later, later))

	assert.Contains(t, list("__complete", ""), "counter\tCounts its runs\n")
	assert.Equal(t, 2, countRuns(t, runs), "a changed module is asked again")

	assert.Equal(t, "counter - Counts its runs\n", list("modules", "list"))
	assert.Equal(t, 2, countRuns(t, runs))
}

// TestDescribeCacheKeepsNoFailure checks that a module that fails is asked
// again next time, and that the cache is not read for a module with a
// manifest.
func TestDescribeCacheKeepsNoFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cache := filepath.Join(t.TempDir(), "cache")
	runs := filepath.Join(t.TempDir(), "runs")
	writeModule(t, dir, "flaky", `echo run >> "`+runs+`"; exit 1`, "")

	for range 2 {
		tree := newTree(t)

		tree.cacheDir = cache

		_, err := tree.mount(dir)
		require.NoError(t, err)

		_, err = tree.run("modules", "list")
		require.NoError(t, err)
		assert.Len(t, tree.log.Messages(), 1)
	}

	assert.Equal(t, 2, countRuns(t, runs))
}
