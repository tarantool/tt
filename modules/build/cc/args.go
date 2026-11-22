package cc

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build/v3"
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
		return cxxName
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

// defaultCFLAGS are the flags a build compiles a source with when the flags
// of its language in the environment, CFLAGS or CXXFLAGS, are unset: the
// optimization of LuaRocks, and the debug information, as the cmake backend
// builds RelWithDebInfo and autoconf defaults to -g -O2, so that a module
// that crashes Tarantool has a backtrace. tt package pack --debuginfo strip
// takes it out of what is shipped.
var defaultCFLAGS = []string{"-O2", "-g"}

// compileFlags are the flags a source of the language lang is compiled
// with, of a build for goos:
//
//	[-O2 -g unless CFLAGS is set] -fPIC \
//	    [-ffile-prefix-map=<project>=. -gno-record-gcc-switches] \
//	    <cflags...> <$CFLAGS...> -I<Tarantool incdir> -D<define...> -I<incdir...> \
//	    <pkg-config --cflags...>
//
// for C and assembler, and the same with cxxflags and CXXFLAGS for C++. The
// flags that keep the paths of the machine out of the object are those the
// compiler of the language takes (toolchain.reproducible), whatever CFLAGS
// is, so that a module built from the same sources in another directory is
// the same. What pkg-config gives for the packages of pkg_config (pkg) is of
// both.
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
func compileFlags(config Config, tools toolchain, env envFlags, pkg pkgFlags, goos string,
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
		args = append(args, defaultCFLAGS...)
	}

	args = append(args, "-fPIC")
	args = append(args, tools.reproducibleOf(lang)...)
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

	return append(args, pkg.cflags...)
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

// linksLibstdcxx reports whether the link of a build for target, with
// static = true when static is set, of C++ when cxx is, by the compiler of
// the words linker links the static C++ library (staticLibstdcxx): on Linux,
// static and C++, unless linker is zig, whose zig c++ links its own libc++
// into every module, statically and its symbols hidden, ignores
// -static-libstdc++ and refuses -Wl,--exclude-libs.
func linksLibstdcxx(target build.Target, static, cxx bool, linker []string) bool {
	return static && cxx && target.OS == "linux" && !isZig(linker)
}

// isZig reports whether the compiler of words is zig cc or zig c++: one of
// its words is a program named zig, the next one cc or c++. The words tell
// it, not what the compiler prints: zig c++ --version says "clang version
// 22.1.8", which a clang says too, and zig is a program run with cc or
// c++, so its name is there whatever runs it first, ccache zig cc say. A
// script of another name that runs zig is not told.
func isZig(words []string) bool {
	for index, word := range words[:max(len(words)-1, 0)] {
		name := strings.TrimSuffix(filepath.Base(word), ".exe")
		if name == "zig" && slices.Contains(zigCompilers, words[index+1]) {
			return true
		}
	}

	return false
}

// symbolicFunctions is the flag that binds the calls of a module of Linux
// to the functions it defines to those, at its link: without it the loader
// resolves a call to a function of a default visibility by the order of
// the process, the executable first, and a module defining a function of
// the name of one Tarantool exports - Tarantool Enterprise 3.7 exports 1373,
// curl_easy_init and 80 more of curl among them - gets that of Tarantool
// called even from within itself. Variables are left as they are, as the
// packages of Ubuntu are linked with it by default.
const symbolicFunctions = "-Wl,-Bsymbolic-functions"

// bindsLocally reports whether the link of a module for target by the
// compiler of the words linker binds the calls of the module to its own
// functions (symbolicFunctions): on Linux, but by zig cc and zig c++, whose
// lld refuses -Bsymbolic-functions as an unsupported linker argument. ld.bfd,
// gold, lld and mold of a gcc or a clang take it. A two-level namespace
// of macOS binds a call of a module, a bundle or a dylib of zig
// (linksDylib), to a function it defines already.
func bindsLocally(target build.Target, linker []string) bool {
	return target.OS == "linux" && !isZig(linker)
}

// linksDylib reports whether the module for target is linked by the
// compiler of the words linker as a dylib (zigDylibFlags) rather than with
// libFlag of its toolchain: on darwin, by zig cc and zig c++, whose linker
// crashes on -bundle -undefined dynamic_lookup, and takes -bundle without
// -undefined for the link of a program, one that needs _main. zig has no
// bundle, only executables and dylibs.
func linksDylib(target build.Target, linker []string) bool {
	return target.OS == darwinOS && isZig(linker)
}

// zigDylibFlags are the flags that link the module out of darwin by zig cc
// or zig c++ (linksDylib) in place of libFlag: -shared makes a dylib, which
// dlopen, and so require and package.loadlib, load as they load a bundle,
// the symbols of Tarantool resolved when it is loaded as those of a bundle
// are. zig names a dylib by its -o, a path of the build machine, so
// -install_name names it @rpath/<the file of out>, as the copy of a library
// of bundle is named (darwinBundleCommands).
func zigDylibFlags(out string) []string {
	return []string{
		"-shared", "-undefined", "dynamic_lookup", "-all_load",
		"-Wl,-install_name,@rpath/" + filepath.Base(out),
	}
}

// zigCompilers are the commands of zig that are compilers of C and C++.
var zigCompilers = []string{"cc", cxxName}

// cxxName is what the tools name C++: the language of -x c++, the C++
// compiler of a system, c++, and that of zig, zig c++.
const cxxName = "c++"

// linkArgs builds the argv of the link of objects into out, without the
// words of the compiler that links:
//
//	<LIBFLAG...> [-Wl,-Bsymbolic-functions] \
//	    [-static-libstdc++ -Wl,--exclude-libs,ALL] <ldflags...> \
//	    -o <out> <objects...> \
//	    -L<libdir...> {-l<lib> | <file>}... <pkg-config --libs...> \
//	    -Wl,-rpath,<rpath...> <$LDFLAGS...>
//
// The project's ldflags go before the objects, as make's
// $(CC) $(LDFLAGS) -o out objs $(LDLIBS) has them, so that an option like
// -Wl,--as-needed acts on the libraries that follow; LDFLAGS of the
// environment ends the line, as go-luarocks has it. The rest mirrors the
// builtin build of go-luarocks. The flags of the compilers are not given:
// a flag both need, -pthread or -fsanitize=address, is in cflags or cxxflags
// and in ldflags. LIBFLAG is libFlag of tools, or the flags of a dylib of zig
// (zigDylibFlags) when dylib is set (linksDylib). The calls of the module to
// its own functions are bound to them when symbolic is set (bindsLocally),
// and the static C++ library (staticLibstdcxx) is linked when staticCXX is
// set (linksLibstdcxx).
//
// ldflags and rpath come from config, each merged (append-only) with its
// overlay of platforms for goos, and the libraries from libs
// (planLibraries): -L of library_dirs, libraries in their order, as link
// order matters, then what pkg-config gives for the packages of pkg_config,
// as the libraries of a package may need those of libraries. A directory of
// library_dirs is where the linker looks, and no rpath: one of the build
// machine baked into a module shipped elsewhere names a directory that is
// not there, and a relative one would be resolved against the working
// directory of the process. The loader looks where rpath says, $ORIGIN the
// directory of the module (rpathFlag).
func linkArgs(config Config, tools toolchain, env envFlags, libs libraryArgs, goos string,
	symbolic, staticCXX, dylib bool, objects []string, out string,
) []string {
	overlay := config.Platforms[goos]

	args := make([]string, 0, initialArgsCap+len(objects))

	if dylib {
		args = append(args, zigDylibFlags(out)...)
	} else {
		args = append(args, tools.libFlag...)
	}

	if symbolic {
		args = append(args, symbolicFunctions)
	}

	if staticCXX {
		args = append(args, staticLibstdcxx...)
	}

	args = append(args, config.Ldflags...)
	args = append(args, overlay.Ldflags...)
	args = append(args, "-o", out)
	args = append(args, objects...)
	args = append(args, libs.args...)

	for _, entry := range slices.Concat(config.Rpath, overlay.Rpath, libs.rpath) {
		args = append(args, rpathFlag(entry, tools.origin))
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

// linkerOf are the words of the compiler that links the module of config
// with tools: CXX when a source is C++, CC otherwise.
func linkerOf(config Config, tools toolchain) []string {
	for _, source := range config.Sources {
		if lang, _ := languageOf(source); lang == langCXX {
			return tools.cxx
		}
	}

	return tools.cc
}

// planOf is the plan of the build of config in c, with the toolchain tools,
// the flags of the environment env, those pkg-config gave, pkg, and the
// libraries of the link, libs: one compile a source, in the order of
// sources, and the link of the objects in that order into <module><ext> in
// the output directory.
func planOf(config Config, tools toolchain, env envFlags, pkg pkgFlags, libs libraryArgs,
	c *build.Context,
) plan {
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
			flags:    compileFlags(config, tools, env, pkg, c.Target.OS, lang),
		}

		built.steps = append(built.steps, step)
		paths = append(paths, step.object)
	}

	built.linker = linkerOf(config, tools)
	built.link = concat(built.linker,
		linkArgs(config, tools, env, libs, c.Target.OS, bindsLocally(c.Target, built.linker),
			linksLibstdcxx(c.Target, c.Static, built.cxx, built.linker),
			linksDylib(c.Target, built.linker), paths, built.out))

	return built
}

// commandLine is argv as a line of a log or of an error.
func commandLine(argv []string) string {
	return strings.Join(argv, " ")
}
