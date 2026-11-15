package cc

import (
	"debug/elf"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// This file warns of a module that exports a symbol of the name of one the
// Tarantool of the build exports too.

// shownClashes is how many symbols the warning of clashes names.
const shownClashes = 5

// sttGnuIfunc is STT_GNU_IFUNC, the type of a function of ELF resolved at
// load time, as glibc has some.
const sttGnuIfunc = elf.SymType(10)

// exportedSymbols reads the names of the functions and the variables the
// file at path exports, defined and visible to the loader: of ELF, the
// dynamic symbols of type FUNC, OBJECT or GNU_IFUNC, global or weak, of the
// default visibility, a protected one binding within the file; of Mach-O,
// the external symbols defined in a section (readSymbols), of every slice
// of a universal file.
func exportedSymbols(path string) ([]string, error) {
	file, err := elf.Open(path)
	if err != nil {
		var elfFormat *elf.FormatError

		if !errors.As(err, &elfFormat) && !truncated(err) {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		archs, err := readSymbols(path)
		if err != nil {
			return nil, err
		}

		var names []string

		for _, arch := range archs {
			names = append(names, arch.defined...)
		}

		return names, nil
	}

	defer func() { _ = file.Close() }()

	dynamic, err := file.DynamicSymbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return nil, fmt.Errorf("reading the dynamic symbols of %s: %w", path, err)
	}

	var names []string

	for _, symbol := range dynamic {
		switch elf.ST_TYPE(symbol.Info) {
		case elf.STT_FUNC, elf.STT_OBJECT, sttGnuIfunc:
		default:
			continue
		}

		bind := elf.ST_BIND(symbol.Info)
		if bind != elf.STB_GLOBAL && bind != elf.STB_WEAK {
			continue
		}

		if symbol.Section == elf.SHN_UNDEF || symbol.Section == elf.SHN_ABS ||
			elf.ST_VISIBILITY(symbol.Other) != elf.STV_DEFAULT {
			continue
		}

		names = append(names, symbol.Name)
	}

	return names, nil
}

// clashingSymbols are those of module, the symbols a module exports, that
// tarantool, those the executable exports, has too, sorted, each once, but
// entry, the entry point of a lua-c module, which require finds in the
// module itself whoever else has one of its name.
func clashingSymbols(module, tarantool []string, entry string) []string {
	exported := make(map[string]bool, len(tarantool))
	for _, name := range tarantool {
		exported[name] = true
	}

	var clashes []string

	for _, name := range module {
		if exported[name] && name != entry && !slices.Contains(clashes, name) {
			clashes = append(clashes, name)
		}
	}

	slices.Sort(clashes)

	return clashes
}

// clashWarning is the warning of a module out that exports clashes, symbols
// the tarantool executable exports too.
func clashWarning(out, executable string, clashes []string) string {
	names := strings.Join(clashes, ", ")
	if len(clashes) > shownClashes {
		names = fmt.Sprintf("%s and %d more", strings.Join(clashes[:shownClashes], ", "),
			len(clashes)-shownClashes)
	}

	return fmt.Sprintf("%s exports %d symbols that the tarantool %s exports too: %s; "+
		"the loader resolves a name to one symbol, that of Tarantool, for the other modules, "+
		"ffi, and the module itself where its calls are not bound to its own: make them "+
		"static, or hidden (__attribute__((visibility(\"hidden\"))) or -fvisibility=hidden)",
		out, len(clashes), executable, names)
}

// checkClashes warns when the module just linked exports a symbol the
// Tarantool executable of the build exports too (clashingSymbols). It says
// nothing when there is no executable, or when either file cannot be read:
// the check is to tell of a clash, not to fail a build.
func (r runner) checkClashes() {
	executable := r.c.Tarantool.Executable
	if executable == "" {
		return
	}

	log := logger(r.c)
	out := r.built.out

	module, err := exportedSymbols(out)
	if err != nil {
		log.Debug("the symbols of " + out + " are not checked: " + err.Error())

		return
	}

	tarantool, err := exportedSymbols(executable)
	if err != nil {
		log.Debug("the symbols of " + out + " are not checked against " + executable + ": " +
			err.Error())

		return
	}

	entry := ""
	if r.required != "" {
		entry = luaEntry(r.required)
	}

	clashes := clashingSymbols(module, tarantool, entry)
	if len(clashes) > 0 {
		warner(r.c)(clashWarning(out, executable, clashes))
	}
}
