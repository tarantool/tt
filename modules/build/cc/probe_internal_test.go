package cc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pickyCC is a compiler that fails a compile with -ffile-prefix-map, warns
// of -gno-record-gcc-switches, as gcc warns of a flag it does not know, and
// compiles as fakecc does otherwise.
const pickyCC = `#!/bin/sh
case "$*" in
*-ffile-prefix-map=*) echo "error: unrecognized option" >&2; exit 1 ;;
*-gno-record-gcc-switches*) echo "warning: ignored option" >&2 ;;
esac
exec fakecc "$@"
`

// TestReproducibleFlags pins the flags that keep the paths of the machine
// out of the objects: the project mapped to ".", and no command line.
func TestReproducibleFlags(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"-ffile-prefix-map=/home/me/app=.", "-gno-record-gcc-switches"},
		reproducibleFlags("/home/me/app"))
	assert.Equal(t, []string{"-gno-record-gcc-switches"}, reproducibleFlags(""))
}

// TestProbeReproducible checks that a compile takes the reproducible flags
// its compiler takes, a flag the compiler fails or warns of left out, that
// the answers are probed once and kept in the work directory, and that a
// build from scratch probes again.
func TestProbeReproducible(t *testing.T) {
	t.Parallel()

	harness, bld := fakeHarness(t, C(), map[string]any{"module": "m", "sources": []any{"a.c"}},
		map[string]string{"CC": "pickycc"})

	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Tools, "pickycc"),
		[]byte(pickyCC), 0o700))
	writeFile(t, harness, "a.c", "/* a.c */\n")

	probes := func() int {
		count := 0

		for _, call := range allCalls(t, harness) {
			if isFlagProbe(call) {
				count++
			}
		}

		return count
	}

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

	compiles := calls(t, harness)
	require.Len(t, compiles, 2)
	assert.NotContains(t, compiles[0], "-ffile-prefix-map", "a flag the compiler fails")
	assert.NotContains(t, compiles[0], "-gno-record-gcc-switches", "a flag it warns of")
	// The probe pickycc fails does not reach fakecc, which records the runs.
	assert.Equal(t, 1, probes())
	assert.FileExists(t, filepath.Join(harness.Context.Dirs.Work, probeFile))

	writeFile(t, harness, "a.c", "/* a.c changed */\n")
	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Equal(t, 1, probes(), "the answers are kept")

	harness.Context.Fresh = true

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Equal(t, 2, probes(), "a build from scratch probes again")

	// A compiler that takes them, fakecc, gets both: it is another
	// compiler, and its object another one.
	harness.Context.Fresh = false
	bld.lookupEnv = func(key string) (string, bool) { return "fakecc", key == "CC" }

	before := len(calls(t, harness))

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

	compiles = calls(t, harness)[before:]
	require.Len(t, compiles, 2)
	assert.Contains(t, compiles[0], " "+reproducible(harness)+" ")
}
