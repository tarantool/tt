package instances_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/instances"
)

// fuzzSeeds cover every branch of the format: tt2 records, every field, the
// refused YAML features and broken names, so that mutations start next to
// each of them.
var fuzzSeeds = []string{
	"",
	"storage-master:\n\nstorage-replica:\n",
	"router:\n  advertise_uri: localhost:3301\n  http_port: 8081\n",
	"legacy-1: {script: init.lua, restart: always}\nstorage-1: {}\n",
	`worker-1:
  binary: ./bin/worker
  args: [--port, 8080, ""]
  env:
    LOG_LEVEL: info
    RATIO: 1.50
  workdir: /var/lib/worker
  stop_signal: SIGINT
  stop_timeout: 1m30s
  restart: on-failure
`,
	"a: &w {binary: w}\nb: *w\nc: {<<: *w, args: [*w]}\n",
	"a:\n---\nb:\n",
	"'': {}\n..:\na/b:\n~:\n? [a]\n: {}\n",
	"a: {env: {A: !!binary aGk=, '': x, B=C: d}, args: [1, ~, [x]], stop_timeout: 0s}\n",
	"a: {binary: a, script: b, stop_signal: sigterm, restart: false}\na:\n",
}

// FuzzParse checks Parse neither panics nor returns a result that breaks
// what the package documents, whatever the input: a description is either
// refused with ErrInvalid or yields records a consumer can use unchecked.
func FuzzParse(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := instances.Parse(data, appDir)
		if err != nil {
			require.ErrorIs(t, err, instances.ErrInvalid)
			require.Nil(t, got)

			return
		}

		again, err := instances.Parse(data, appDir)
		require.NoError(t, err)
		require.Equal(t, got, again, "the result is not deterministic")

		names := make([]string, 0, len(got))
		for i := range got {
			checkInstance(t, &got[i])

			names = append(names, got[i].Name)
		}

		slices.Sort(names)
		assert.Len(t, slices.Compact(names), len(got), "a name is declared twice")
	})
}

// checkInstance checks one record keeps the promises of Instance.
func checkInstance(t *testing.T, inst *instances.Instance) {
	t.Helper()

	assert.NotEmpty(t, inst.Name)
	assert.NotContains(t, []string{".", ".."}, inst.Name)
	assert.NotContains(t, inst.Name, "/")
	assert.False(t, strings.ContainsFunc(inst.Name, unicode.IsControl), "name %q", inst.Name)

	for _, path := range []string{inst.Script, inst.Workdir} {
		if path != "" {
			assert.True(t, filepath.IsAbs(path), "path %q is not absolute", path)
		}
	}

	// A binary is either an absolute path or a program name for PATH, which
	// has no slash and is not a directory name.
	if inst.Binary != "" && !filepath.IsAbs(inst.Binary) {
		assert.NotContains(t, inst.Binary, "/", "binary %q is a relative path", inst.Binary)
		assert.NotContains(t, []string{".", ".."}, inst.Binary)
	}

	assert.False(t, inst.Binary != "" && inst.Script != "", "both binary and script are set")
	assert.Contains(t, []instances.RestartPolicy{
		instances.RestartNo, instances.RestartOnFailure, instances.RestartAlways,
	}, inst.Restart)
	assert.GreaterOrEqual(t, inst.StopTimeout, time.Duration(0))

	for name := range inst.Env {
		assert.NotEmpty(t, name)
		assert.NotContains(t, name, "=")
	}
}
