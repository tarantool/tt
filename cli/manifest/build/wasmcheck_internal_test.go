package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeWasm(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	return path
}

func TestCheckWasmFiles(t *testing.T) {
	t.Parallel()

	component := writeWasm(t, "app.wasm", []byte("\x00asm\x0d\x00\x01\x00rest"))
	module := writeWasm(t, "core.wasm", []byte("\x00asm\x01\x00\x00\x00rest"))
	other := writeWasm(t, "notes.txt", []byte("not checked"))

	warnings, err := checkWasmFiles([]string{component, other})
	require.NoError(t, err)
	assert.Empty(t, warnings)

	warnings, err = checkWasmFiles([]string{module})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "core WebAssembly module")
}

func TestCheckWasmFilesRejectsOthers(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"text.wasm":    []byte("hello, this is not wasm"),
		"short.wasm":   []byte("\x00as"),
		"version.wasm": []byte("\x00asm\x02\x00\x00\x00"),
	} {
		_, err := checkWasmFiles([]string{writeWasm(t, name, data)})
		require.ErrorIs(t, err, errNotWasm, name)
	}
}
