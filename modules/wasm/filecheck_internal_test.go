package wasm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/sdktest"
)

func writeWasm(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	return path
}

func TestCheckWasmFile(t *testing.T) {
	t.Parallel()

	component := writeWasm(t, "app.wasm", []byte("\x00asm\x0d\x00\x01\x00rest"))
	module := writeWasm(t, "core.wasm", []byte("\x00asm\x01\x00\x00\x00rest"))

	warning, err := checkWasmFile(component)
	require.NoError(t, err)
	assert.Empty(t, warning)

	warning, err = checkWasmFile(module)
	require.NoError(t, err)
	assert.Equal(t, module+" is a core WebAssembly module, not a component: "+
		"require('wasm') loads components only", warning)
}

func TestCheckWasmFileRejectsOthers(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"text.wasm":    []byte("hello, this is not wasm"),
		"short.wasm":   []byte("\x00as"),
		"version.wasm": []byte("\x00asm\x02\x00\x00\x00"),
	} {
		path := writeWasm(t, name, data)

		_, err := checkWasmFile(path)
		require.ErrorIs(t, err, errNotWasm, name)
		assert.Contains(t, err.Error(), path, name)
	}

	_, err := checkWasmFile(filepath.Join(t.TempDir(), "missing.wasm"))
	require.ErrorContains(t, err, "checking ")
}

// TestModuleChecksTheWasmFiles checks that the module gives the core the
// check of the .wasm files, by their suffix.
func TestModuleChecksTheWasmFiles(t *testing.T) {
	t.Parallel()

	module := New(sdktest.New(t))

	require.Len(t, module.FileChecks, 1)
	assert.Equal(t, ".wasm", module.FileChecks[0].Suffix)
	assert.Empty(t, module.Backends)

	warning, err := module.FileChecks[0].Check(
		writeWasm(t, "core.wasm", []byte("\x00asm\x01\x00\x00\x00")))
	require.NoError(t, err)
	assert.Contains(t, warning, "core WebAssembly module")
}
