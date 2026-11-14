package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
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

// TestCheckWasmFilesAsAFileCheck checks that the check of the .wasm files
// is a file check of a module as it stands: checkWasmFiles of one file at a
// time, given to checkFiles, says of any list of files what checkWasmFiles
// says of it - the same warnings, in the same order, and the same first
// error, with the warnings dropped - so that the module wasm can provide it
// as its FileCheck with no more of the core.
func TestCheckWasmFilesAsAFileCheck(t *testing.T) {
	t.Parallel()

	check := sdkbuild.FileCheck{Suffix: wasmSuffix, Check: func(path string) (string, error) {
		warnings, err := checkWasmFiles([]string{path})
		if err != nil || len(warnings) == 0 {
			return "", err
		}

		return warnings[0], nil
	}}

	component := writeWasm(t, "app.wasm", []byte("\x00asm\x0d\x00\x01\x00rest"))
	module := writeWasm(t, "core.wasm", []byte("\x00asm\x01\x00\x00\x00rest"))
	other := writeWasm(t, "notes.txt", []byte("not checked"))
	text := writeWasm(t, "text.wasm", []byte("hello, this is not wasm"))
	short := writeWasm(t, "short.wasm", []byte("\x00as"))

	for _, files := range [][]string{
		{component, other},
		{module, component, module},
		{module, text, module},
		{other, short, text},
		nil,
	} {
		want, wantErr := checkWasmFiles(files)
		got, err := checkFiles([]sdkbuild.FileCheck{check}, files)

		assert.Equal(t, want, got, files)

		if wantErr == nil {
			require.NoError(t, err, files)
		} else {
			require.EqualError(t, err, wantErr.Error(), files)
		}
	}
}
