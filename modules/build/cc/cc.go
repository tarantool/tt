// Package cc is the c and lua-c build backends: one driver of the C and C++
// compilers that builds the sources of a component into one native module,
// <module>.so (.dylib on macOS), a stored-procedure module of
// box.schema.func for c and a Lua C extension, with its luaopen_<module>,
// for lua-c. The two differ in how Tarantool loads the module only, which
// is not the driver's concern.
//
// Each source is compiled on its own into an object of the work directory
// of the build, the compiler of its language chosen by its extension, and
// the objects are linked into the module, with the C++ compiler when one of
// them is C++: what make does with the rules of a Makefile, and what
// compile_commands.json, written beside the objects, tells an editor.
//
// The compilers, the flags that link a module and the headers of Tarantool
// are the toolchain of the build, which the driver derives from the
// Context, as go-luarocks derives that of a builtin rock (toolchainOf): CC
// and CXX of the environment tt runs in, the flags of the target, the
// headers of the Tarantool of the build (Tarantool.IncludeDir). The flags of the
// component come from its block, and CFLAGS, CXXFLAGS and LDFLAGS from the
// environment tt runs in, last.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package cc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk/build"
)

const (
	// CName is the name of the c backend.
	CName = "c"
	// LuaCName is the name of the lua-c backend.
	LuaCName = "lua-c"
)

// ErrMissingHeaders reports that the build knows no headers of Tarantool
// (build.Tarantool.IncludeDir), so a c or lua-c component cannot be
// compiled. It wraps build.ErrNoHeaders.
var ErrMissingHeaders = fmt.Errorf("%w: cannot compile c/lua-c component", build.ErrNoHeaders)

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
		New: build.Typed(func(cfg *Config, component build.Component, common build.Common) (
			build.Build, error,
		) {
			return newBuild(name, cfg, component, common)
		}),
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
		// CC and CXX of the machine compile for the machine, and no
		// compiler of another platform is looked for.
		Cross: false,
	}
}

// Config are the fields of a c or lua-c build block.
type Config struct {
	// Module is the name of the module: the file is <module>.so in the
	// namespace of the component, a dotted name kept as it is. Required.
	Module string `toml:"module"`
	// Sources are the C, C++ and assembler files compiled, relative to the
	// working directory, each of a language its extension names
	// (languageOf). Required.
	Sources []string `toml:"sources"`
	// IncludeDirs are the directories of headers, -I.
	IncludeDirs []string `toml:"include_dirs"`
	// Libraries are linked, -l, in their order.
	Libraries []string `toml:"libraries"`
	// LibraryDirs are where they are, -L: where the linker looks, not where
	// the loader does (Rpath).
	LibraryDirs []string `toml:"library_dirs"`
	// Rpath are the directories the loader looks for the libraries of the
	// module in, -Wl,-rpath,<dir>, each absolute or under $ORIGIN, the
	// directory of the module (rpathFlag).
	Rpath []string `toml:"rpath"`
	// PkgConfig are the packages pkg-config gives the flags of, each as
	// pkg-config takes it, "openssl" or "libsasl2 >= 2.1": those of
	// --cflags go to every compile, those of --libs to the link, after
	// libraries (pkgconfig.go).
	PkgConfig []string `toml:"pkg_config"`
	// Link are the components of the package whose libraries the module
	// links: what the output of their builds sends to = "link", in
	// build.Dirs.LinkOf, -L and -l of each library, -I of its include/
	// (link.go).
	Link []string `toml:"link"`
	// Bundle are the libraries the module links whose shared libraries are
	// copied beside it, each by its name as libraries has it, which the
	// module finds there by an rpath of its directory (bundle.go). lua-c
	// only.
	Bundle []string `toml:"bundle"`
	// Defines are the macros defined, -D.
	Defines []string `toml:"defines"`
	// Cflags are flags of the C compiler, of the C and assembler sources,
	// after tt's own and before CFLAGS of the environment, which win a
	// conflict.
	Cflags []string `toml:"cflags"`
	// Cxxflags are flags of the C++ compiler, of the C++ sources, after
	// tt's own and before CXXFLAGS of the environment.
	Cxxflags []string `toml:"cxxflags"`
	// Ldflags are flags of the link, before the objects, so that an option
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
	Rpath       []string `toml:"rpath"`
	PkgConfig   []string `toml:"pkg_config"`
	Defines     []string `toml:"defines"`
	Cflags      []string `toml:"cflags"`
	Cxxflags    []string `toml:"cxxflags"`
	Ldflags     []string `toml:"ldflags"`
}

// newBuild checks cfg, the block of a build of the backend name: module and
// sources are required, each source is of a language tt compiles and listed
// once, env sets none of the variables that reach nothing there (envVars),
// each overlay of platforms is for an OS tt builds for, and no flag of the
// compilers or of the linker, of the build or of an overlay, is empty, which
// cc would take for the name of a file, and link names components other
// than the component, each once (checkLink).
func newBuild(name string, config *Config, component build.Component, common build.Common) (
	build.Build, error,
) {
	if config.Module == "" {
		return nil, build.FieldErrorf("", "module is required for backend %q", name)
	}

	if len(config.Sources) == 0 {
		return nil, build.FieldErrorf("", "sources is required for backend %q", name)
	}

	err := checkSources(config.Sources)
	if err != nil {
		return nil, err
	}

	for _, variable := range envVars {
		if _, set := common.Env[variable.name]; set {
			return nil, build.FieldErrorf("env."+variable.name, "does nothing on backend %q: %s",
				name, variable.instead)
		}
	}

	flags := []flagList{
		{"cflags", config.Cflags}, {"cxxflags", config.Cxxflags}, {"ldflags", config.Ldflags},
		{"pkg_config", config.PkgConfig}, {"libraries", config.Libraries},
	}
	rpaths := []flagList{{"rpath", config.Rpath}}
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
			flagList{prefix + "cxxflags", overlay.Cxxflags},
			flagList{prefix + "ldflags", overlay.Ldflags},
			flagList{prefix + "pkg_config", overlay.PkgConfig},
			flagList{prefix + "libraries", overlay.Libraries})
		rpaths = append(rpaths, flagList{prefix + "rpath", overlay.Rpath})
	}

	for _, list := range flags {
		if slices.Contains(list.flags, "") {
			return nil, build.FieldErrorf(list.name, "must not contain an empty entry")
		}
	}

	for _, list := range rpaths {
		err = checkRpath(list)
		if err != nil {
			return nil, err
		}
	}

	err = checkLink(config.Link, component.Name)
	if err == nil {
		err = checkBundle(config.Bundle, name)
	}

	if err != nil {
		return nil, err
	}

	return ccBuild{name: name, config: *config, lookupEnv: os.LookupEnv}, nil
}

// Links names the components whose libraries the build links, link, which
// the core checks the products build before it.
func (b ccBuild) Links() []string {
	return slices.Clone(b.config.Link)
}

// How the loaders name the directory of the module in an rpath: $ORIGIN of
// Linux, which an entry of rpath writes as well on darwin, and @loader_path
// of darwin.
const (
	originToken = "$ORIGIN"
	loaderPath  = "@loader_path"
)

// originTokens are how an entry of rpath names the directory of the module:
// $ORIGIN and ${ORIGIN}, as the loader of Linux spells it.
var originTokens = []string{"${ORIGIN}", originToken}

// checkRpath checks the entries of an rpath list: none is empty, each is an
// absolute directory or one under $ORIGIN (originTokens), and none has a
// comma, which would split -Wl,-rpath,<entry> into more arguments of the
// linker. A relative directory is refused: the loader resolves it against
// the working directory of the process that loads the module, not the
// directory of the module, which $ORIGIN is.
func checkRpath(list flagList) error {
	for index, entry := range list.flags {
		field := fmt.Sprintf("%s[%d]", list.name, index)

		switch {
		case entry == "":
			return build.FieldErrorf(list.name, "must not contain an empty entry")
		case strings.Contains(entry, ","):
			return build.FieldErrorf(field, "%q has a comma, which -Wl,-rpath,<dir> "+
				"would split", entry)
		case !filepath.IsAbs(entry) && !originRelative(entry):
			return build.FieldErrorf(field, "%q is relative, which the loader resolves "+
				"against the working directory of the process: write $ORIGIN/%s for a "+
				"directory beside the module, or an absolute one", entry, entry)
		}
	}

	return nil
}

// originRelative reports whether the entry of rpath is $ORIGIN, or a path
// under it.
func originRelative(entry string) bool {
	for _, token := range originTokens {
		rest, ok := strings.CutPrefix(entry, token)
		if ok && (rest == "" || rest[0] == '/') {
			return true
		}
	}

	return false
}

// rpathFlag is the flag of the link that has the loader look for the
// libraries of the module in the directory entry of rpath: -Wl,-rpath,<entry>,
// $ORIGIN made origin, the way the loader of the target names the directory
// of the module (toolchain.origin). The argument reaches the linker as it
// is, with no shell to expand $ORIGIN.
func rpathFlag(entry, origin string) string {
	for _, token := range originTokens {
		if rest, ok := strings.CutPrefix(entry, token); ok {
			entry = origin + rest

			break
		}
	}

	return "-Wl,-rpath," + entry
}

// checkSources checks that each of sources is of a language tt compiles
// (languageOf) and that no file is listed twice, which would compile it
// twice into one object and define its symbols twice.
func checkSources(sources []string) error {
	seen := make(map[string]int, len(sources))

	for index, source := range sources {
		field := fmt.Sprintf("sources[%d]", index)

		if _, ok := languageOf(source); !ok {
			return build.FieldErrorf(field, "%q is not a source tt compiles: want a name "+
				"ending in %s; a prebuilt library goes into libraries, by name or as a file",
				source, orList(sourceExtensions()))
		}

		clean := filepath.Clean(source)
		if first, listed := seen[clean]; listed {
			return build.FieldErrorf(field, "%q is sources[%d] again", source, first)
		}

		seen[clean] = index
	}

	return nil
}

// flagList is a list of flags of a build, with the name of its field.
type flagList struct {
	name  string
	flags []string
}

// envVars are the variables of make and autotools a user would set in env
// of a c or lua-c build to compile it with, with what to write instead. The
// compiler reads none of them, so one of env reaches nothing: CC, CXX,
// CFLAGS, CXXFLAGS, LDFLAGS and PKG_CONFIG tt reads from the environment it
// runs in to put the command lines together, CPPFLAGS, LIBS and LDLIBS from
// nowhere, since the build has fields of its own for what they carry. The
// variables pkg-config reads itself, PKG_CONFIG_PATH say, are not among
// them: pkg-config runs with the environment of the build, env included.
var envVars = []struct {
	name    string
	instead string
}{
	{"CC", "the compiler is CC of the environment tt runs in, cc by default"},
	{"CXX", "the C++ compiler is CXX of the environment tt runs in, c++ by default"},
	{"CFLAGS", "put the flags of the compiler into cflags; " +
		"CFLAGS of the environment tt runs in comes after them"},
	{"CXXFLAGS", "put the flags of the C++ compiler into cxxflags; " +
		"CXXFLAGS of the environment tt runs in comes after them"},
	{"CPPFLAGS", "put the macros into defines, the directories of headers into " +
		"include_dirs and other flags of the preprocessor into cflags and cxxflags"},
	{"LDFLAGS", "put the flags of the link into ldflags; " +
		"LDFLAGS of the environment tt runs in comes after them"},
	{"LIBS", "put the libraries into libraries and their directories into library_dirs"},
	{"LDLIBS", "put the libraries into libraries and their directories into library_dirs"},
	{"PKG_CONFIG", "pkg-config is PKG_CONFIG of the environment tt runs in, pkg-config " +
		"by default; PKG_CONFIG_PATH in env reaches it"},
}

// ccBuild is a checked c or lua-c build block.
type ccBuild struct {
	// name is the name of the backend, c or lua-c.
	name   string
	config Config
	// lookupEnv reads the environment tt runs in, for CC, CXX, CFLAGS,
	// CXXFLAGS and LDFLAGS: os.LookupEnv outside tests.
	lookupEnv func(string) (string, bool)
}

// Preflight checks that the build has the headers of a Tarantool to compile
// against: a tt with no Tarantool builds what needs none, and the build
// would otherwise fail only once its turn came, saying nothing of why. It
// checks that pkg-config is there and knows the packages of pkg_config too.
func (b ccBuild) Preflight(ctx context.Context, c *build.Context) error {
	tools, err := b.toolchain(c)
	if err != nil {
		return err
	}

	return checkPkgConfig(ctx, c, tools, pkgPackages(b.config, c.Target.OS))
}

// Run compiles each source into its object in the work directory, writes
// compile_commands.json there, and links the objects into <module><ext> in
// the output directory, the extension the one Tarantool loads on the target
// (build.Target.ModuleExt). A dotted module is a file a.b.so, not a
// directory a. An object whose inputs are those of its last compile is not
// compiled again, nor the module linked again when its objects and the link
// are those of the last one and it is still there (cache.go).
func (b ccBuild) Run(ctx context.Context, c *build.Context) error {
	// What Preflight checks of pkg-config, the run finds as it asks it for
	// the flags.
	tools, err := b.toolchain(c)
	if err != nil {
		return err
	}

	if c.Fresh {
		err = dropCache(c.Dirs.Work)
		if err != nil {
			return err
		}
	}

	packages := pkgPackages(b.config, c.Target.OS)

	pkg, err := pkgConfigFlags(ctx, c, tools, packages, c.Static)
	if err != nil {
		return err
	}

	linked, err := linkedLibrariesOf(b.config.Link, c)
	if err != nil {
		return err
	}

	// The headers of the libraries of link come after include_dirs, before
	// those of pkg-config, as their libraries come.
	pkg.cflags = concat(linked.includeFlags(), pkg.cflags)

	linker := linkerOf(b.config, tools)

	bundled, err := bundledLibrariesOf(ctx, c, b.config, pkg, linked, linker)
	if err != nil {
		return err
	}

	libs := planLibraries(b.config, c.Target.OS, c.Dirs.Cwd, pkg, linked, bundled, c.Static,
		staticFinder(ctx, c, linker))

	env := environFlags(b.lookupEnv)
	identities := identify(ctx, c, planOf(b.config, tools, env, pkg, libs, c).compilers())

	// The plan again, with the flags its compilers take.
	probeReproducible(ctx, c, &tools, planOf(b.config, tools, env, pkg, libs, c), identities)

	built := planOf(b.config, tools, env, pkg, libs, c)

	err = writeCompileCommands(c.Dirs.Work, c.Dirs.Cwd, built.steps)
	if err != nil {
		return err
	}

	err = removeStaleObjects(objectDir(c.Dirs.Work), built.steps)
	if err != nil {
		return err
	}

	run := newRunner(ctx, c, built, identities)

	if len(packages) > 0 {
		run.keyEnv = pkgConfigEnv
	}

	if b.name == LuaCName {
		run.required = requireName(c.Component.Namespace, b.config.Module)
	}

	err = compileAll(ctx, built.steps, maxJobs(c), run.compile)

	// The warnings of the compiles that succeeded are shown when another
	// failed too: they may tell why.
	run.output.show(built.steps, warner(c))

	if err != nil {
		return err
	}

	err = run.link(ctx, libs.files)
	if err != nil {
		return err
	}

	return bundled.finish(c, built.out)
}

// toolchain returns the toolchain of the build in c (toolchainOf), checking
// that it has the headers of a Tarantool.
func (b ccBuild) toolchain(c *build.Context) (toolchain, error) {
	tools, err := toolchainOf(c, b.lookupEnv)
	if err != nil {
		return toolchain{}, err
	}

	if tools.includeDir == "" {
		if c.Tarantool.Prefix != "" {
			// A Tarantool without headers: the core adds where it looked
			// for them and how to point tt at them.
			return toolchain{}, ErrMissingHeaders
		}

		return toolchain{}, fmt.Errorf("%w: %s", ErrMissingHeaders, noTarantoolHint)
	}

	return tools, nil
}

// runner runs the compiles and the link of a plan, each unless it is up to
// date.
type runner struct {
	c     *build.Context
	built plan
	// environ is the environment of the commands of the build.
	environ []string
	// identities are the compilerIdentity of each compiler of the plan, by
	// its command line.
	identities map[string]string
	hashes     *fileHashes
	// required is the name require loads the module by, of lua-c, whose
	// module is checked once it is linked (checkLuaModule); empty for c.
	required string
	// output is what the compilers printed, of the compiles of this build
	// and of the records of the objects up to date.
	output *compilerOutput
	// keyEnv are the variables of the environment of the build that are of
	// the key of every object and of the link besides those the compilers
	// read: those of pkg-config, of a build that runs it (pkgConfigEnv).
	keyEnv []string
}

// newRunner returns the runner of built in c, whose compilers are of
// identities (identify).
func newRunner(ctx context.Context, c *build.Context, built plan,
	identities map[string]string,
) runner {
	return runner{
		c: c, built: built, environ: c.Command(ctx, built.linker[0]).Env,
		identities: identities, hashes: newFileHashes(), required: "",
		output: newCompilerOutput(), keyEnv: nil,
	}
}

// identify returns the compilerIdentity of each of compilers, by its command
// line, each probed once a build.
func identify(ctx context.Context, c *build.Context, compilers [][]string) map[string]string {
	identities := make(map[string]string, len(compilers))

	for _, words := range compilers {
		identities[commandLine(words)] = compilerIdentity(ctx, c, words)
	}

	return identities
}

// compile compiles the object of step unless it is up to date, and records
// it. What the compiler printed, of this compile or of the one the record
// of an object up to date is of, is kept for the build to show.
func (r runner) compile(ctx context.Context, step compileStep) error {
	c := r.c
	key := objectKey(step, c.Dirs.Cwd, c.Target, r.identities[commandLine(step.compiler)],
		envOf(r.environ, concat(languageEnv(step.lang), r.keyEnv)))

	if record, ok := upToDate(step, key, r.hashes); ok {
		logger(c).DebugContext(ctx, step.source+" is up to date: "+step.object)
		r.output.add(step, record.Output)

		return nil
	}

	// The depfile goes too: a compiler that writes none, a wrapper that
	// drops -MD, would leave that of the last compile to be taken for its.
	for _, path := range []string{step.recordPath(), step.depfile()} {
		err := removeRecord(path)
		if err != nil {
			return err
		}
	}

	printed, err := compile(ctx, c, step)
	if err != nil {
		return err
	}

	r.output.add(step, printed)

	// The object is made: one that cannot be recorded is compiled again
	// next time, which fails no build.
	err = recordObject(step, c.Dirs.Cwd, key, printed, r.hashes)
	if err != nil {
		logger(c).DebugContext(ctx, "the object of "+step.source+" is not cached: "+err.Error())
	}

	return nil
}

// link links the module unless it is up to date, libraries being the files
// of the libraries of the build that are of the key (libraryArgs.files). A
// module up to date is touched, so that the core takes it as made by this
// build too.
func (r runner) link(ctx context.Context, libraries []string) error {
	c := r.c
	work := c.Dirs.Work
	out := r.built.out

	key, err := linkKey(r.built, c.Target, r.identities[commandLine(r.built.linker)],
		envOf(r.environ, concat(linkEnv, r.keyEnv)), libraries, r.hashes)
	if err != nil {
		return err
	}

	if linkUpToDate(work, out, key) {
		logger(c).DebugContext(ctx, out+" is up to date")

		now := time.Now()

		err = os.Chtimes(out, now, now)
		if err != nil {
			return fmt.Errorf("touching %s: %w", out, err)
		}

		return nil
	}

	err = removeRecord(filepath.Join(work, linkRecordFile))
	if err != nil {
		return err
	}

	err = runLink(ctx, c, r.built.link)
	if err != nil {
		return err
	}

	// The record comes after the checks: a module that fails them is
	// linked, and checked, again by the next build.
	if r.required != "" {
		err = r.checkLuaModule(ctx)
		if err != nil {
			return err
		}
	}

	err = r.testLoad(ctx)
	if err != nil {
		return err
	}

	r.checkClashes()

	return recordLink(work, out, key)
}

// checkLuaModule checks the module of lua-c just linked (checkLuaModule). A
// file that is neither ELF nor Mach-O, or that cannot be read, is not
// checked: the check is to tell why a module would not load, not to fail a
// build of a toolchain it does not know.
func (r runner) checkLuaModule(ctx context.Context) error {
	out := r.built.out

	symbols, err := readSymbols(out)
	if err != nil {
		logger(r.c).DebugContext(ctx, "the entry point of "+out+" is not checked: "+err.Error())

		return nil //nolint:nilerr // A module the check cannot read is not checked.
	}

	return checkLuaModule(out, r.required, symbols)
}

// warner is the Warn of the build of c, one that logs the warning at the
// debug level for a Context made without one.
func warner(c *build.Context) func(string) {
	if c.Warn != nil {
		return c.Warn
	}

	return func(warning string) {
		logger(c).Debug(warning)
	}
}

// logger is the logger of the build of c, one that discards for a Context
// made without one.
func logger(c *build.Context) *slog.Logger {
	if c.Log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return c.Log
}

// toolchain is how tt compiles a C module Tarantool loads, as go-luarocks
// compiles one of a builtin rock.
type toolchain struct {
	// cc is the C compiler, the program and the arguments it is run with
	// first: CC of the environment tt runs in split into words
	// (splitWords), cc by default. It compiles the C and assembler sources
	// and links a module with no C++ source.
	cc []string
	// cxx is the C++ compiler, in words as cc is: CXX of the environment,
	// c++ by default. It compiles the C++ sources and links a module with
	// one.
	cxx []string
	// pkgConfig is pkg-config, in words as cc is: PKG_CONFIG of the
	// environment, pkg-config by default.
	pkgConfig []string
	// libFlag are the flags that link a module Tarantool loads: -shared,
	// or -bundle -undefined dynamic_lookup -all_load on darwin, whose
	// loader resolves the symbols of Tarantool when it loads the module.
	// zig cc and zig c++ link a dylib on darwin instead (linksDylib).
	libFlag []string
	// origin is how the loader of the target names the directory of the
	// module in an rpath: $ORIGIN, @loader_path on darwin.
	origin string
	// includeDir is the directory of the headers of Tarantool,
	// build.Tarantool.IncludeDir: include/tarantool under its prefix, empty
	// when module.h is not there.
	includeDir string
	// reproducible are the flags that keep the paths of the machine out of
	// the objects (reproducibleFlags) the compiler of each language takes,
	// langC of C and assembler, langCXX of C++, probed (probeReproducible);
	// none before they are.
	reproducible map[language][]string
}

// toolchainOf returns the toolchain of the build of c, CC, CXX and
// PKG_CONFIG read with lookup. A value that splitWords cannot split is an
// error; one of no words is unset.
func toolchainOf(c *build.Context, lookup func(string) (string, bool)) (toolchain, error) {
	tools := toolchain{
		cc: []string{"cc"}, cxx: []string{cxxName}, pkgConfig: []string{pkgConfigName},
		libFlag: []string{"-shared"}, origin: originToken, includeDir: "", reproducible: nil,
	}

	for _, compiler := range []struct {
		variable string
		words    *[]string
	}{{"CC", &tools.cc}, {"CXX", &tools.cxx}, {"PKG_CONFIG", &tools.pkgConfig}} {
		words, err := compilerOf(lookup, compiler.variable)
		if err != nil {
			return toolchain{}, err
		}

		if len(words) > 0 {
			*compiler.words = words
		}
	}

	if c.Target.OS == "darwin" {
		tools.libFlag = []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"}
		tools.origin = loaderPath
	}

	tools.includeDir = c.Tarantool.IncludeDir

	return tools, nil
}

// compilerOf returns the words of the program that variable, CC, CXX or
// PKG_CONFIG, of the environment tt runs in names, read with lookup: none
// when it is unset or blank.
func compilerOf(lookup func(string) (string, bool), variable string) ([]string, error) {
	value, _ := lookup(variable)

	words, err := splitWords(value)
	if err == nil && len(words) > 0 && words[0] == "" {
		err = errNoProgram
	}

	if err != nil {
		return nil, fmt.Errorf("%s=%q: %w", variable, value, err)
	}

	return words, nil
}

// errNoProgram reports a compiler whose first word, the program, is empty.
var errNoProgram = errors.New("the first word, the program, is empty")

// envFlags are the flags of the environment tt runs in that the command
// lines take: CFLAGS, CXXFLAGS and LDFLAGS, split on blanks as make splits
// them. The compiler reads none of them itself.
type envFlags struct {
	// cflagsSet reports that CFLAGS is set, if to nothing: tt's -O2 -g of
	// the C sources is then left out, as autoconf leaves out its own -g -O2.
	cflagsSet bool
	cflags    []string
	// cxxflagsSet is cflagsSet of CXXFLAGS and the C++ sources.
	cxxflagsSet bool
	cxxflags    []string
	ldflags     []string
}

// environFlags reads the envFlags with lookup, os.LookupEnv outside tests.
func environFlags(lookup func(string) (string, bool)) envFlags {
	cflags, cflagsSet := lookup("CFLAGS")
	cxxflags, cxxflagsSet := lookup("CXXFLAGS")
	ldflags, _ := lookup("LDFLAGS")

	return envFlags{
		cflagsSet:   cflagsSet,
		cflags:      strings.Fields(cflags),
		cxxflagsSet: cxxflagsSet,
		cxxflags:    strings.Fields(cxxflags),
		ldflags:     strings.Fields(ldflags),
	}
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
