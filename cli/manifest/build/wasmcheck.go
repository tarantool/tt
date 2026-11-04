package build

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// wasmSuffix ends the name of a WebAssembly file a component lays out.
const wasmSuffix = ".wasm"

// wasmHeaderSize is the size of the preamble of a WebAssembly binary: the
// magic and a version, which the component model splits into a version and
// a layer.
const wasmHeaderSize = 8

var (
	// wasmMagic opens every WebAssembly binary.
	wasmMagic = []byte("\x00asm")
	// wasmCoreModule and wasmComponent are the preambles after the magic: a
	// core module is version 1, layer 0; a component is version 0x0d,
	// layer 1.
	wasmCoreModule = []byte{0x01, 0x00, 0x00, 0x00}
	wasmComponent  = []byte{0x0d, 0x00, 0x01, 0x00}

	// errNotWasm reports a .wasm file that is not a WebAssembly binary.
	errNotWasm = errors.New("not a WebAssembly binary")
)

// checkWasmFiles checks the .wasm files among the files the components laid
// out, as the build of an executable component checks its programs: a file
// that is no WebAssembly binary, an output a build mistook for one, fails the
// build. A core module is laid out with a warning: tarantool-wasm loads
// components only, but another loader may well want a module.
func checkWasmFiles(paths []string) ([]string, error) {
	var warnings []string

	for _, path := range paths {
		if !strings.HasSuffix(path, wasmSuffix) {
			continue
		}

		header, err := readWasmHeader(path)
		if err != nil {
			return nil, err
		}

		switch {
		case bytes.Equal(header[len(wasmMagic):], wasmComponent):
		case bytes.Equal(header[len(wasmMagic):], wasmCoreModule):
			warnings = append(warnings, fmt.Sprintf("%s is a core WebAssembly module, "+
				"not a component: require('wasm') loads components only", path))
		default:
			return nil, fmt.Errorf("%s: %w of a known version (preamble % x)",
				path, errNotWasm, header[len(wasmMagic):])
		}
	}

	return warnings, nil
}

// readWasmHeader reads the preamble of the WebAssembly binary at path.
func readWasmHeader(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	header := make([]byte, wasmHeaderSize)

	_, err = io.ReadFull(file, header)
	if err != nil || !bytes.Equal(header[:len(wasmMagic)], wasmMagic) {
		return nil, fmt.Errorf("%s: %w", path, errNotWasm)
	}

	return header, nil
}
