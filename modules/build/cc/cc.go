// Package cc is the c and lua-c build backends: one driver of the C
// compiler that builds the sources of a component into one native module,
// <module>.so (.dylib on macOS), a stored-procedure module of
// box.schema.func for c and a Lua C extension, with its luaopen_<module>,
// for lua-c. The two differ in how Tarantool loads the module only, which
// is not the driver's concern.
//
// The compiler, the flags that link a module and the headers of Tarantool
// are the toolchain of the build, which the driver derives from the
// Context, as go-luarocks derives that of a builtin rock (toolchainOf):
// CC of the environment tt runs in, the flags of the target, the headers
// under the prefix of the Tarantool of the build. The flags of the
// component come from its block, and CFLAGS and LDFLAGS from the
// environment tt runs in, last.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package cc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

const (
	// CName is the name of the c backend.
	CName = "c"
	// LuaCName is the name of the lua-c backend.
	LuaCName = "lua-c"
)

// ErrMissingHeaders reports that the build knows no headers of Tarantool,
// as it knows no prefix of it (build.Tarantool.Prefix), so a c or lua-c
// component cannot be compiled.
var ErrMissingHeaders = errors.New(
	"tarantool development headers not found: cannot compile c/lua-c component")

// noTarantoolHint says why a build has no headers of Tarantool, and how to
// give it some.
const noTarantoolHint = "no tarantool to take them from, neither in .tarantool/bin " +
	"of the project nor in PATH; tt binary install tarantool installs one"

// Backends returns the c and lua-c backends.
func Backends() []build.Backend {
	return []build.Backend{C(), LuaC()}
}

// C returns the c backend.
func C() build.Backend {
	return backend(CName)
}

// LuaC returns the lua-c backend.
func LuaC() build.Backend {
	return backend(LuaCName)
}

// backend returns the backend name, of the driver of cc.
func backend(name string) build.Backend {
	return build.Backend{
		Name:     name,
		Produces: []build.Result{build.Module},
		Config:   func() any { return new(Config) },
		New: func(cfg any, _ build.Component, common build.Common) (build.Build, error) {
			return newBuild(name, cfg, common)
		},
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
	}
}

// Config are the fields of a c or lua-c build block.
type Config struct {
	// Module is the name of the module: the file is <module>.so in the
	// namespace of the component, a dotted name kept as it is. Required.
	Module string `toml:"module"`
	// Sources are the C files compiled, relative to the working directory.
	// Required.
	Sources []string `toml:"sources"`
	// IncludeDirs are the directories of headers, -I.
	IncludeDirs []string `toml:"include_dirs"`
	// Libraries are linked, -l, in their order.
	Libraries []string `toml:"libraries"`
	// LibraryDirs are where they are, -L, with an rpath where the toolchain
	// has one.
	LibraryDirs []string `toml:"library_dirs"`
	// Defines are the macros defined, -D.
	Defines []string `toml:"defines"`
	// Cflags are flags of the compiler, after tt's own and before CFLAGS of
	// the environment, which win a conflict.
	Cflags []string `toml:"cflags"`
	// Ldflags are flags of the link, before the sources, so that an option
	// acts on the libraries that follow them, and before LDFLAGS of the
	// environment.
	Ldflags []string `toml:"ldflags"`
	// Platforms add to the lists above for one OS, by its GOOS name.
	Platforms map[string]Overlay `toml:"platforms"`
}

// Overlay is the part of a c or lua-c build for one OS
// ([components.<name>.build.platforms.<os>]): each list goes after the one
// of the build, for that OS only.
type Overlay struct {
	IncludeDirs []string `toml:"include_dirs"`
	Libraries   []string `toml:"libraries"`
	LibraryDirs []string `toml:"library_dirs"`
	Defines     []string `toml:"defines"`
	Cflags      []string `toml:"cflags"`
	Ldflags     []string `toml:"ldflags"`
}

// newBuild checks cfg, the block of a build of the backend name: module and
// sources are required, env sets none of the variables that reach nothing
// there (envVars), each overlay of platforms is for an OS tt builds for, and
// no flag of the compiler or of the linker, of the build or of an overlay,
// is empty, which cc would take for the name of a file.
func newBuild(name string, cfg any, common build.Common) (build.Build, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
	}

	if config.Module == "" {
		return nil, build.FieldErrorf("", "module is required for backend %q", name)
	}

	if len(config.Sources) == 0 {
		return nil, build.FieldErrorf("", "sources is required for backend %q", name)
	}

	for _, variable := range envVars {
		if _, set := common.Env[variable.name]; set {
			return nil, build.FieldErrorf("env."+variable.name, "does nothing on backend %q: %s",
				name, variable.instead)
		}
	}

	flags := []flagList{{"cflags", config.Cflags}, {"ldflags", config.Ldflags}}
	oses := build.SupportedOS()

	for _, goos := range slices.Sorted(maps.Keys(config.Platforms)) {
		// A part for an OS tt builds for none of the platforms of would never
		// be added: platforms.macos for darwin, say.
		if !slices.Contains(oses, goos) {
			return nil, build.FieldErrorf("platforms."+goos,
				"%q is not an OS tt builds for (want %s)", goos, orList(oses))
		}

		overlay := config.Platforms[goos]
		prefix := "platforms." + goos + "."

		flags = append(flags,
			flagList{prefix + "cflags", overlay.Cflags},
			flagList{prefix + "ldflags", overlay.Ldflags})
	}

	for _, list := range flags {
		if slices.Contains(list.flags, "") {
			return nil, build.FieldErrorf(list.name, "must not contain an empty entry")
		}
	}

	return ccBuild{config: *config, lookupEnv: os.LookupEnv}, nil
}

// flagList is a list of flags of a build, with the name of its field.
type flagList struct {
	name  string
	flags []string
}

// envVars are the variables of make and autotools a user would set in env
// of a c or lua-c build to compile it with, with what to write instead. The
// compiler reads none of them, so one of env reaches nothing: CC, CFLAGS and
// LDFLAGS tt reads from the environment it runs in to put the cc line
// together, CPPFLAGS, LIBS and LDLIBS from nowhere, since the build has
// fields of its own for what they carry.
var envVars = []struct {
	name    string
	instead string
}{
	{"CC", "the compiler is CC of the environment tt runs in, cc by default"},
	{"CFLAGS", "put the flags of the compiler into cflags; " +
		"CFLAGS of the environment tt runs in comes after them"},
	{"CPPFLAGS", "put the macros into defines, the directories of headers into " +
		"include_dirs and other flags of the preprocessor into cflags"},
	{"LDFLAGS", "put the flags of the link into ldflags; " +
		"LDFLAGS of the environment tt runs in comes after them"},
	{"LIBS", "put the libraries into libraries and their directories into library_dirs"},
	{"LDLIBS", "put the libraries into libraries and their directories into library_dirs"},
}

// ccBuild is a checked c or lua-c build block.
type ccBuild struct {
	config Config
	// lookupEnv reads the environment tt runs in, for CC, CFLAGS and
	// LDFLAGS: os.LookupEnv outside tests.
	lookupEnv func(string) (string, bool)
}

// Preflight checks that the build has the headers of a Tarantool to compile
// against: a tt with no Tarantool builds what needs none, and the build
// would otherwise fail only once its turn came, saying nothing of why.
func (b ccBuild) Preflight(_ context.Context, c *build.Context) error {
	if toolchainOf(c, b.lookupEnv).includeDir == "" {
		return fmt.Errorf("%w: %s", ErrMissingHeaders, noTarantoolHint)
	}

	return nil
}

// Run compiles the module with one cc command into <module><ext> in the
// output directory, the extension the one Tarantool loads on the target
// (build.Target.ModuleExt). A dotted module is a file a.b.so, not a
// directory a.
func (b ccBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err != nil {
		return err
	}

	tools := toolchainOf(c, b.lookupEnv)
	out := filepath.Join(c.Dirs.Modules, b.config.Module+c.Target.ModuleExt())
	args := ccArgs(b.config, tools, environFlags(b.lookupEnv), out, c.Target.OS)

	return c.Run(c.Command(ctx, tools.cc, args...))
}

// toolchain is how tt compiles a C module Tarantool loads, as go-luarocks
// compiles one of a builtin rock.
type toolchain struct {
	// cc is the C compiler: CC of the environment tt runs in, cc by
	// default.
	cc string
	// libFlag are the flags that link a module Tarantool loads: -shared,
	// or -bundle -undefined dynamic_lookup -all_load on darwin, whose
	// loader resolves the symbols of Tarantool when it loads the module.
	libFlag []string
	// rpath reports whether each -L<dir> is followed by -Wl,-rpath,<dir>:
	// not on darwin.
	rpath bool
	// includeDir is the directory of the headers of Tarantool under its
	// prefix, include/tarantool, as the compiler is given it whether or not
	// module.h is there; empty when the build knows no prefix.
	includeDir string
}

// toolchainOf returns the toolchain of the build of c, CC read with lookup.
func toolchainOf(c *build.Context, lookup func(string) (string, bool)) toolchain {
	tools := toolchain{
		cc: "cc", libFlag: []string{"-shared"}, rpath: true, includeDir: "",
	}

	if compiler, _ := lookup("CC"); compiler != "" {
		tools.cc = compiler
	}

	if c.Target.OS == "darwin" {
		tools.libFlag = []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"}
		tools.rpath = false
	}

	if c.Tarantool.Prefix != "" {
		tools.includeDir = filepath.Join(c.Tarantool.Prefix, "include", "tarantool")
	}

	return tools
}

// initialArgsCap pre-sizes the cc argument slice to avoid early regrows.
const initialArgsCap = 16

// defaultCFLAGS is the optimization a build compiles with when CFLAGS of the
// environment is unset, as LuaRocks does.
const defaultCFLAGS = "-O2"

// envFlags are the flags of the environment tt runs in that the cc command
// line takes: CFLAGS and LDFLAGS, split on blanks as make splits them. The
// compiler reads neither itself.
type envFlags struct {
	// cflagsSet reports that CFLAGS is set, if to nothing: tt's -O2 is then
	// left out, as autoconf leaves out its own -g -O2.
	cflagsSet bool
	cflags    []string
	ldflags   []string
}

// environFlags reads the envFlags with lookup, os.LookupEnv outside tests.
func environFlags(lookup func(string) (string, bool)) envFlags {
	cflags, set := lookup("CFLAGS")
	ldflags, _ := lookup("LDFLAGS")

	return envFlags{
		cflagsSet: set,
		cflags:    strings.Fields(cflags),
		ldflags:   strings.Fields(ldflags),
	}
}

// ccArgs builds the cc argv:
//
//	[-O2 unless CFLAGS is set] -fPIC <cflags...> <$CFLAGS...> \
//	    -I<Tarantool incdir> -D<define...> -I<incdir...> <LIBFLAG...> \
//	    <ldflags...> -o <out> <sources...> \
//	    {-L<libdir> [-Wl,-rpath,<libdir> with an rpath]}... -l<lib...> <$LDFLAGS...>
//
// The flags of the compiler come in three parts: tt's defaults, the
// project's (cflags) and the user's (CFLAGS of the environment), the last
// winning a conflict, as CFLAGS comes after AM_CFLAGS in autotools: CFLAGS
// of "-O0 -g" builds for a debugger, -Wno-error undoes the project's
// -Werror. The project's ldflags go before the sources, as make's
// $(CC) $(LDFLAGS) -o out objs $(LDLIBS) has them, so that an option like
// -Wl,--as-needed acts on the libraries that follow; LDFLAGS of the
// environment ends the line, as go-luarocks has it. The rest mirrors the
// builtin build of go-luarocks.
//
// cflags / ldflags / defines / include dirs / library dirs / libraries come
// from config, each merged (append-only) with its overlay of platforms for
// goos; library order is preserved because link order matters. Each -L is
// immediately followed by its -Wl,-rpath, entry when the toolchain has
// rpaths. out is the path of the module; sources are given as they are and
// resolved against the working directory by the compiler. It is a pure
// function so the argv is testable without a compiler or Tarantool headers.
func ccArgs(config Config, tools toolchain, env envFlags, out, goos string) []string {
	overlay := config.Platforms[goos]

	defines := concat(config.Defines, overlay.Defines)
	includeDirs := concat(config.IncludeDirs, overlay.IncludeDirs)
	libraryDirs := concat(config.LibraryDirs, overlay.LibraryDirs)
	libraries := concat(config.Libraries, overlay.Libraries)

	args := make([]string, 0, initialArgsCap)

	if !env.cflagsSet {
		args = append(args, defaultCFLAGS)
	}

	args = append(args, "-fPIC")
	args = append(args, config.Cflags...)
	args = append(args, overlay.Cflags...)
	args = append(args, env.cflags...)

	if tools.includeDir != "" {
		args = append(args, "-I"+tools.includeDir)
	}

	for _, d := range defines {
		args = append(args, "-D"+d)
	}

	for _, inc := range includeDirs {
		args = append(args, "-I"+inc)
	}

	args = append(args, tools.libFlag...)
	args = append(args, config.Ldflags...)
	args = append(args, overlay.Ldflags...)
	args = append(args, "-o", out)
	args = append(args, config.Sources...)

	for _, dir := range libraryDirs {
		args = append(args, "-L"+dir)
		if tools.rpath {
			args = append(args, "-Wl,-rpath,"+dir)
		}
	}

	for _, lib := range libraries {
		args = append(args, "-l"+lib)
	}

	return append(args, env.ldflags...)
}

// concat returns base followed by extra in a fresh slice (an append-only
// overlay merge that never mutates either input).
func concat(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))

	out = append(out, base...)

	return append(out, extra...)
}

// orList joins names as a sentence lists them: "a", "a or b", "a, b or c".
func orList(names []string) string {
	last := len(names) - 1
	if last <= 0 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " or " + names[last]
}
