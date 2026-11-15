package wasm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// wasmSuffix ends the name of a WebAssembly file a package lays out.
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

// checkWasmFile is the file check of the module (sdk/build.FileCheck) for
// the .wasm files of a package, whatever component made them - laid out from
// its sources or written by its build - as the build checks the programs a
// component makes: a file that is no WebAssembly binary, an output a build
// mistook for one, fails the build. A core module is laid out with a
// warning: tarantool-wasm loads components only, but another loader may well
// want a module. It reads the preamble of the file only, so it does not
// look at ctx.
func checkWasmFile(_ context.Context, path string) (string, error) {
	header, err := readWasmHeader(path)
	if err != nil {
		return "", err
	}

	switch {
	case bytes.Equal(header[len(wasmMagic):], wasmComponent):
		return "", nil
	case bytes.Equal(header[len(wasmMagic):], wasmCoreModule):
		return fmt.Sprintf("%s is a core WebAssembly module, not a component: "+
			"require('wasm') loads components only", path), nil
	default:
		return "", fmt.Errorf("%s: %w of a known version (preamble % x)",
			path, errNotWasm, header[len(wasmMagic):])
	}
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
