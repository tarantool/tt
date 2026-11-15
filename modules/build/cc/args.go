package cc

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// language is the language of a source, which picks its compiler and its
// flags.
type language int

const (
	// langC is C, of .c: compiled with CC and cflags.
	langC language = iota
	// langCXX is C++, of .cc, .cpp, .cxx and .c++: compiled with CXX and
	// cxxflags.
	langCXX
	// langAsmCpp is assembler the preprocessor runs on first, of .S:
	// compiled, as C is, with CC and cflags, which take -D and -I as well.
	langAsmCpp
	// langAsm is assembler as it is, of .s: assembled by CC, with cflags,
	// and no preprocessor, so the compiler writes no list of headers.
	langAsm
)

// String is the name the compilers give the language (-x): c, c++,
// assembler-with-cpp, assembler.
func (l language) String() string {
	switch l {
	case langC:
		return "c"
	case langCXX:
		return "c++"
	case langAsmCpp:
		return "assembler-with-cpp"
	case langAsm:
		return "assembler"
	default:
		return "unknown"
	}
}

// sourceLanguages are the languages of the extensions of sources, as gcc and
// clang tell them by the extension. The extensions are case-sensitive, as
// they are to gcc: .C, which gcc takes for C++, is left out, since on the
// default file systems of macOS a.C and a.c are one file, and .cp and .CPP,
// which few projects use, with it. Assembler is compiled as before, when
// every source went to cc alike; a header, an object or a library is no
// source: cc linked them into the module before, which a prebuilt library
// of libraries and library_dirs does.
var sourceLanguages = []struct {
	ext  string
	lang language
}{
	{".c", langC},
	{".cc", langCXX}, {".cpp", langCXX}, {".cxx", langCXX}, {".c++", langCXX},
	{".S", langAsmCpp}, {".s", langAsm},
}

// languageOf returns the language of a source by its extension, and false
// for one of no language tt compiles.
func languageOf(source string) (language, bool) {
	ext := filepath.Ext(source)

	for _, known := range sourceLanguages {
		if known.ext == ext {
			return known.lang, true
		}
	}

	return 0, false
}

// sourceExtensions are the extensions of sourceLanguages, in their order.
func sourceExtensions() []string {
	exts := make([]string, 0, len(sourceLanguages))

	for _, known := range sourceLanguages {
		exts = append(exts, known.ext)
	}

	return exts
}

// objectDirName is the directory of the work directory the objects are in.
const objectDirName = "obj"

// objectDir is the directory of the objects of a build whose work directory
// is work.
func objectDir(work string) string {
	return filepath.Join(work, objectDirName)
}

// objectHashLen is the number of hex digits of the hash of the path of a
// source in the name of its object.
const objectHashLen = 8

// objectName is the name of the object of source, in the directory of the
// objects: its base name, a hash of its path as the build lists it, cleaned,
// and .o, a.c.1b2c3d4e.o. The hash keeps apart the objects of two sources
// of one name, a/x.c and b/x.c, and those of ../x.c and /abs/x.c, which a
// path mirrored under the directory could not hold; the base name tells
// which source an object is of.
func objectName(source string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(source)))

	return filepath.Base(source) + "." + hex.EncodeToString(sum[:])[:objectHashLen] + ".o"
}

// initialArgsCap pre-sizes the argument slices to avoid early regrows.
const initialArgsCap = 16

// defaultCFLAGS is the optimization a build compiles a source with when the
// flags of its language in the environment, CFLAGS or CXXFLAGS, are unset,
// as LuaRocks does.
const defaultCFLAGS = "-O2"

// compileFlags are the flags a source of the language lang is compiled
// with, of a build for goos:
//
//	[-O2 unless CFLAGS is set] -fPIC <cflags...> <$CFLAGS...> \
//	    -I<Tarantool incdir> -D<define...> -I<incdir...>
//
// for C and assembler, and the same with cxxflags and CXXFLAGS for C++.
//
// The flags of the language come in three parts: tt's defaults, the
// project's (cflags or cxxflags) and the user's (CFLAGS or CXXFLAGS of the
// environment), the last winning a conflict, as CFLAGS comes after
// AM_CFLAGS in autotools: CFLAGS of "-O0 -g" builds for a debugger,
// -Wno-error undoes the project's -Werror. The flags of one language do not
// reach the sources of the other: -std=c11 fails a C++ compile, and gcc
// warns of -Wstrict-prototypes there. The macros and the directories of
// headers are of both.
//
// Each list is merged (append-only) with its overlay of platforms for goos.
// It is a pure function, so the flags are testable without a compiler or
// Tarantool headers.
func compileFlags(config Config, tools toolchain, env envFlags, goos string,
	lang language,
) []string {
	overlay := config.Platforms[goos]

	langFlags := concat(config.Cflags, overlay.Cflags)
	envSet, envLang := env.cflagsSet, env.cflags

	if lang == langCXX {
		langFlags = concat(config.Cxxflags, overlay.Cxxflags)
		envSet, envLang = env.cxxflagsSet, env.cxxflags
	}

	args := make([]string, 0, initialArgsCap)

	if !envSet {
		args = append(args, defaultCFLAGS)
	}

	args = append(args, "-fPIC")
	args = append(args, langFlags...)
	args = append(args, envLang...)

	if tools.includeDir != "" {
		args = append(args, "-I"+tools.includeDir)
	}

	for _, d := range concat(config.Defines, overlay.Defines) {
		args = append(args, "-D"+d)
	}

	for _, inc := range concat(config.IncludeDirs, overlay.IncludeDirs) {
		args = append(args, "-I"+inc)
	}

	return args
}

// staticLibstdcxx are the flags that link the C++ library of gcc into a
// module of Linux, of a build with static = true: -static-libstdc++ takes
// libstdc++.a rather than libstdc++.so.6, which a machine Tarantool runs on
// may not have, as Tarantool links it in statically, and
// -Wl,--exclude-libs,ALL keeps the symbols of the archives out of what the
// module exports - hundreds of std:: that the process would otherwise
// resolve to it. Without static the module needs libstdc++.so.6, and
// loads on a machine of an older glibc: the static library of a new gcc
// needs the glibc it was built with (glibc 2.38, say, for arc4random and
// __isoc23_strtoul of gcc 15), so a module that takes it does not load in
// the Tarantool of an older distribution. On macOS libc++ is a library of
// the system, and clang ignores -static-libstdc++.
var staticLibstdcxx = []string{"-static-libstdc++", "-Wl,--exclude-libs,ALL"}

// linkArgs builds the argv of the link of objects into out, without the
// words of the compiler that links:
//
//	<LIBFLAG...> [-static-libstdc++ -Wl,--exclude-libs,ALL] <ldflags...> \
//	    -o <out> <objects...> \
//	    {-L<libdir> [-Wl,-rpath,<libdir> with an rpath]}... -l<lib...> <$LDFLAGS...>
//
// The project's ldflags go before the objects, as make's
// $(CC) $(LDFLAGS) -o out objs $(LDLIBS) has them, so that an option like
// -Wl,--as-needed acts on the libraries that follow; LDFLAGS of the
// environment ends the line, as go-luarocks has it. The rest mirrors the
// builtin build of go-luarocks. The flags of the compilers are not given:
// a flag both need, -pthread or -fsanitize=address, is in cflags or cxxflags
// and in ldflags. The static C++ library (staticLibstdcxx) is linked for a
// module of C++ of Linux with static = true.
//
// ldflags, library dirs and libraries come from config, each merged
// (append-only) with its overlay of platforms for goos; library order is
// preserved because link order matters. Each -L is immediately followed by
// its -Wl,-rpath, entry when the toolchain has rpaths.
func linkArgs(config Config, tools toolchain, env envFlags, target build.Target,
	static, cxx bool, objects []string, out string,
) []string {
	overlay := config.Platforms[target.OS]

	args := make([]string, 0, initialArgsCap+len(objects))

	args = append(args, tools.libFlag...)

	if static && cxx && target.OS == "linux" {
		args = append(args, staticLibstdcxx...)
	}

	args = append(args, config.Ldflags...)
	args = append(args, overlay.Ldflags...)
	args = append(args, "-o", out)
	args = append(args, objects...)

	for _, dir := range concat(config.LibraryDirs, overlay.LibraryDirs) {
		args = append(args, "-L"+dir)
		if tools.rpath {
			args = append(args, "-Wl,-rpath,"+dir)
		}
	}

	for _, lib := range concat(config.Libraries, overlay.Libraries) {
		args = append(args, "-l"+lib)
	}

	return append(args, env.ldflags...)
}

// compileStep is the compile of one source into its object.
type compileStep struct {
	// source is the source as the build lists it, relative to the working
	// directory of the build or absolute.
	source string
	lang   language
	// object is the absolute path of its object.
	object string
	// compiler are the words of the compiler of its language.
	compiler []string
	// flags are its compileFlags.
	flags []string
}

// args returns the argv of the compile of step, the words of its compiler
// first, the object written to out:
//
//	<compiler...> <flags...> -MD -MF <object>.d -MQ <object> -c <source> -o <out>
//
// -MD writes the list of the files the compile read, the headers of the
// system included, which the cache of the objects checks; -MQ names the
// object in it whatever out is, and quotes it as make would.
func (s compileStep) args(out string) []string {
	args := make([]string, 0, len(s.compiler)+len(s.flags)+initialArgsCap)

	args = append(args, s.compiler...)
	args = append(args, s.flags...)

	return append(args, "-MD", "-MF", s.depfile(), "-MQ", s.object, "-c", s.source, "-o", out)
}

// depfile is the path of the list of the files the compile of step read.
func (s compileStep) depfile() string {
	return s.object + ".d"
}

// plan is what a build runs: the compiles, then the link.
type plan struct {
	steps []compileStep
	// cxx reports whether a source is C++, and so the module is linked with
	// the C++ compiler.
	cxx bool
	// out is the path of the module.
	out string
	// linker are the words of the compiler that links, CXX or CC.
	linker []string
	// link is the argv of the link, the words of linker first.
	link []string
}

// compilers are the words of each compiler built runs, the one that links
// included, each once.
func (p plan) compilers() [][]string {
	compilers := [][]string{p.linker}

	for _, step := range p.steps {
		if !slices.ContainsFunc(compilers, func(words []string) bool {
			return slices.Equal(words, step.compiler)
		}) {
			compilers = append(compilers, step.compiler)
		}
	}

	return compilers
}

// planOf is the plan of the build of config in c, with the toolchain tools
// and the flags of the environment env: one compile a source, in the order
// of sources, and the link of the objects in that order into
// <module><ext> in the output directory.
func planOf(config Config, tools toolchain, env envFlags, c *build.Context) plan {
	objects := objectDir(c.Dirs.Work)
	built := plan{
		steps:  make([]compileStep, 0, len(config.Sources)),
		cxx:    false,
		out:    filepath.Join(c.Dirs.Modules, config.Module+c.Target.ModuleExt()),
		linker: nil,
		link:   nil,
	}

	paths := make([]string, 0, len(config.Sources))

	for _, source := range config.Sources {
		lang, _ := languageOf(source)
		compiler := tools.cc

		if lang == langCXX {
			compiler = tools.cxx
			built.cxx = true
		}

		step := compileStep{
			source:   source,
			lang:     lang,
			object:   filepath.Join(objects, objectName(source)),
			compiler: compiler,
			flags:    compileFlags(config, tools, env, c.Target.OS, lang),
		}

		built.steps = append(built.steps, step)
		paths = append(paths, step.object)
	}

	built.linker = tools.cc
	if built.cxx {
		built.linker = tools.cxx
	}

	built.link = concat(built.linker,
		linkArgs(config, tools, env, c.Target, c.Static, built.cxx, paths, built.out))

	return built
}

// commandLine is argv as a line of a log or of an error.
func commandLine(argv []string) string {
	return strings.Join(argv, " ")
}
