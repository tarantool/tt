package cc

import (
	"debug/elf"
	"debug/macho"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// This file checks a module of lua-c once it is linked, so that a module
// require cannot load fails its build rather than its first require.

// luaEntry returns the name of the function require(name) calls in a C
// module, the rule of LuaJIT, whose loader_C calls mksymname
// (third_party/luajit/src/lib_package.c of Tarantool): the part of name up
// to its first "-" dropped, LUA_IGMARK, every "." made "_", and luaopen_
// in front. The searchers of Tarantool (src/lua/loaders.lua) have the same
// mksymname. So require('my-app.fast') calls luaopen_app_fast.
func luaEntry(name string) string {
	if _, after, ok := strings.Cut(name, "-"); ok {
		name = after
	}

	return "luaopen_" + strings.ReplaceAll(name, ".", "_")
}

// requireName is the name a module of the component of namespace is
// required by: <namespace>.<module>, the "/" of a nested namespace made
// ".", the module alone in no namespace, as
// lib/tarantool/<namespace>/<module>.so is found.
func requireName(namespace, module string) string {
	if namespace == "" {
		return module
	}

	return strings.ReplaceAll(namespace, "/", ".") + "." + module
}

// luaAPIPrefixes are the prefixes of the names of the functions of the Lua
// API of Tarantool: lua.h, lauxlib.h, lualib.h, luajit.h and lmisclib.h.
var luaAPIPrefixes = []string{"lua_", "luaL_", "luaopen_", "luaJIT_", "luaM_"}

// cxxLuaAPI reports whether symbol, an undefined symbol of a module, is a
// function of the Lua API by its C++ name: _Z<length><name><parameters>, as
// the Itanium ABI mangles a function of the global namespace, the name one
// of the Lua API (luaAPIPrefixes). A C++ source that includes lua.h rather
// than lua.hpp declares the functions so, since lua.h of Tarantool has no
// extern "C", and the module then needs _Z15luaL_checkstackP9lua_StateiPKc,
// which no Tarantool has. A function of a namespace, _ZN..., is no function
// of the API, nor is one that merely takes a lua_State.
func cxxLuaAPI(symbol string) bool {
	rest, ok := strings.CutPrefix(symbol, "_Z")
	if !ok {
		return false
	}

	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}

	length, err := strconv.Atoi(rest[:digits])
	if err != nil || length <= 0 || digits+length > len(rest) {
		return false
	}

	name := rest[digits : digits+length]

	return slices.ContainsFunc(luaAPIPrefixes, func(prefix string) bool {
		return strings.HasPrefix(name, prefix)
	})
}

// moduleSymbols are what checkLuaModule looks at in a module, or in a slice
// of a universal one: the names of the symbols it exports, defined, and
// those it needs, undefined, as C names them, without the leading _ of
// Mach-O.
type moduleSymbols struct {
	// arch is the architecture of the slice of a universal file; empty for
	// a file of one.
	arch      string
	defined   []string
	undefined []string
}

// errUnknownFormat reports a file that is neither ELF nor Mach-O, which the
// checks of a module skip.
var errUnknownFormat = errors.New("neither ELF nor Mach-O")

// readSymbols reads the symbols of the module at path: of ELF, the dynamic
// symbols; of Mach-O, those of its symbol table that are external, of every
// slice of a universal file, each slice a list of its own.
func readSymbols(path string) ([]moduleSymbols, error) {
	elfFile, err := elf.Open(path)
	if err == nil {
		defer func() { _ = elfFile.Close() }()

		symbols, err := elfSymbols(elfFile)
		if err != nil {
			return nil, err
		}

		return []moduleSymbols{symbols}, nil
	}

	var elfFormat *elf.FormatError

	if !errors.As(err, &elfFormat) && !truncated(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	fat, err := macho.OpenFat(path)
	if err == nil {
		defer func() { _ = fat.Close() }()

		archs := make([]moduleSymbols, 0, len(fat.Arches))

		for _, arch := range fat.Arches {
			symbols := machoSymbols(arch.File)

			symbols.arch = arch.Cpu.String()
			archs = append(archs, symbols)
		}

		return archs, nil
	}

	machoFile, err := macho.Open(path)
	if err == nil {
		defer func() { _ = machoFile.Close() }()

		return []moduleSymbols{machoSymbols(machoFile)}, nil
	}

	var machoFormat *macho.FormatError

	if errors.As(err, &machoFormat) || truncated(err) {
		return nil, fmt.Errorf("reading %s: %w", path, errUnknownFormat)
	}

	return nil, fmt.Errorf("reading %s: %w", path, err)
}

// truncated reports whether err is that of a file shorter than the header
// of a format, which is of no format.
func truncated(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// elfSymbols are the dynamic symbols of file, those the loader sees.
func elfSymbols(file *elf.File) (moduleSymbols, error) {
	dynamic, err := file.DynamicSymbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return moduleSymbols{}, fmt.Errorf("reading the dynamic symbols: %w", err)
	}

	var symbols moduleSymbols

	for _, symbol := range dynamic {
		bind := elf.ST_BIND(symbol.Info)
		if bind != elf.STB_GLOBAL && bind != elf.STB_WEAK {
			continue
		}

		if symbol.Section == elf.SHN_UNDEF {
			symbols.undefined = append(symbols.undefined, symbol.Name)
		} else if elf.ST_VISIBILITY(symbol.Other) == elf.STV_DEFAULT {
			symbols.defined = append(symbols.defined, symbol.Name)
		}
	}

	return symbols, nil
}

// The bits of the type of a symbol of Mach-O (<mach-o/nlist.h>).
const (
	machoStab  = 0xe0
	machoType  = 0x0e
	machoExt   = 0x01
	machoUndef = 0x00
	machoSect  = 0x0e
)

// machoSymbols are the external symbols of file, their leading _ dropped.
func machoSymbols(file *macho.File) moduleSymbols {
	var symbols moduleSymbols

	if file.Symtab == nil {
		return symbols
	}

	for _, symbol := range file.Symtab.Syms {
		if symbol.Type&machoStab != 0 || symbol.Type&machoExt == 0 {
			continue
		}

		name := strings.TrimPrefix(symbol.Name, "_")

		switch symbol.Type & machoType {
		case machoUndef:
			symbols.undefined = append(symbols.undefined, name)
		case machoSect:
			symbols.defined = append(symbols.defined, name)
		}
	}

	return symbols
}

// shownSymbols is how many symbols an error names.
const shownSymbols = 3

// checkLuaModule checks the symbols of each slice of a module of lua-c,
// required by name: that it exports its entry point (luaEntry), and that it
// needs no function of the Lua API by its C++ name (cxxLuaAPI). path names
// the module in the errors; those of the first slice that has any are
// returned.
func checkLuaModule(path, name string, archs []moduleSymbols) error {
	entry := luaEntry(name)

	for _, symbols := range archs {
		where := path
		if symbols.arch != "" {
			where += " (" + symbols.arch + ")"
		}

		var (
			problems []error
			cxx      []string
		)

		for _, symbol := range symbols.undefined {
			if cxxLuaAPI(symbol) && !slices.Contains(cxx, symbol) {
				cxx = append(cxx, symbol)
			}
		}

		if len(cxx) > 0 {
			problems = append(problems, fmt.Errorf("%s: %w: %s: lua.h, lauxlib.h and "+
				"lualib.h of Tarantool declare no extern \"C\", so a C++ source includes "+
				"them through <lua.hpp>, which does, or within extern \"C\" { }",
				where, errCxxLuaAPI, listSymbols(cxx)))
		}

		if !slices.Contains(symbols.defined, entry) {
			problems = append(problems, fmt.Errorf("%s: %w %s, the function require(%q) "+
				"calls: define int %s(lua_State *L), extern \"C\" in C++, or set module "+
				"to the name the module is for", where, errNoEntry, entry, name, entry))
		}

		if len(problems) > 0 {
			return errors.Join(problems...)
		}
	}

	return nil
}

// errCxxLuaAPI and errNoEntry are what checkLuaModule finds wrong.
var (
	errCxxLuaAPI = errors.New("the module needs functions of the Lua API by their C++ names")
	errNoEntry   = errors.New("the module exports no")
)

// listSymbols names the first shownSymbols of symbols, and how many more
// there are.
func listSymbols(symbols []string) string {
	if len(symbols) <= shownSymbols {
		return strings.Join(symbols, ", ")
	}

	return fmt.Sprintf("%s and %d more", strings.Join(symbols[:shownSymbols], ", "),
		len(symbols)-shownSymbols)
}
