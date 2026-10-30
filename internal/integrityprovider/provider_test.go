package integrityprovider_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

func TestExecutableRoots(t *testing.T) {
	dir := t.TempDir()
	runtime := filepath.Join(dir, "versions", "tarantool", "tarantool_3.8.1")
	tarantool := filepath.Join(runtime, "tarantool")
	tt := filepath.Join(dir, "versions", "tt", "tt_3.1.0", "tt")
	link := filepath.Join(dir, "bin", "tarantool")

	for _, path := range []string{tarantool, tt} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	}

	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(tarantool, link))

	resolve := func(path string) string {
		resolved, err := filepath.EvalSymlinks(path)
		require.NoError(t, err)

		return resolved
	}

	// A link stands for the directory of its target; an executable given
	// twice, missing or empty gives one root or none.
	assert.Equal(t, []string{resolve(runtime), resolve(filepath.Dir(tt))},
		integrityprovider.ExecutableRoots(link, "", tt, tarantool,
			filepath.Join(dir, "missing")))
}

func TestSetAndGet(t *testing.T) {
	t.Cleanup(func() { integrityprovider.Set(nil) })

	integrityprovider.Set(integrity.NewRSAProvider())
	assert.Equal(t, integrity.NewRSAProvider(), integrityprovider.Get())

	integrityprovider.Set(nil)
	assert.Equal(t, integrity.NewDisabledProvider(), integrityprovider.Get())
}
