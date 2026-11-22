package cc

import (
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/tarantool/tt/sdk/build"
)

// This file builds a module without tt (build.Exporter): the script of the
// export compiles each source on its own into an object of TT_BUILD_DIR and
// links the objects into the module, as Run does, with the toolchain of
// LuaRocks: CC, CFLAGS and LIBFLAG, and CXX, CXXFLAGS, LDFLAGS and
// PKG_CONFIG of the environment. The words of each command are those Run
// puts together, by the same functions - compileFlags, planLibraries and
// linkArgs - given marks (exportMarks) where the script has a variable of
// sh, the value of which only the machine that builds the rock knows. The
// text of the script is the template script of export.sh.tmpl.

// Export returns the script that makes the module Run makes, by the name Run
// gives it, in TT_OUTPUT_DIR:
//
//   - the headers of Tarantool are TT_TARANTOOL_INCLUDE_DIR, LUA_INCDIR of
//     LuaRocks; a build without them fails, as Run does;
//   - a C or assembler source compiles with $CC and the flags of
//     compileFlags, CFLAGS being that of LuaRocks, so the default -O2 -g of
//     Run is left out, as Run leaves it out when CFLAGS is set; a C++ one
//     with ${CXX:-c++} and CXXFLAGS, -O2 when the environment has none;
//   - the objects are linked by the compiler linkerOf says, with $LIBFLAG
//     and $TT_MODULE_LDFLAGS, -Wl,-Bsymbolic-functions on Linux, in place of
//     the flags of the target Run takes;
//   - the lists of platforms.<os> are the arm of the OS in a case on
//     TT_PLATFORM_OS, and the directory of the module in rpath is $ORIGIN,
//     or @loader_path on macOS;
//   - pkg-config, PKG_CONFIG of the environment, runs where the rock is
//     built, as Run runs it;
//   - the libraries a component of link made are looked for in
//     "$TT_LINK_ROOT/<component>" when the build runs, as Run looks for them
//     in build.Dirs.LinkOf.
//
// The program of each compiler the script runs, the first word of CC, CXX
// or PKG_CONFIG, is looked for in PATH by the script itself before it
// compiles: Tools has none, as the variables are the machine's, and hold
// words of a wrapper, CC="ccache cc", that command -v does not take. A
// variable of flags is split on blanks and is not globbed, as Run splits
// CFLAGS; so are CC, CXX, PKG_CONFIG and what pkg-config prints, which Run
// splits as a shell does: a quote in them is a character of a word to the
// script. Lost says what the script lacks against Run: the cache,
// compile_commands.json, the checks of the module, -g and the flags that
// keep the paths of the project out of the objects.
//
// A build of static = true and one of bundle are made with tt alone
// (build.ErrNoExport): tt takes the static libraries of the first, checks
// that it needs nothing of the machine, and links the C++ library into it,
// and copies the shared libraries of the second beside the module, none of
// which the script does.
func (b ccBuild) Export(*build.ExportContext) (build.Export, error) {
	switch {
	case b.static:
		return build.Export{}, build.NoExportf("static", "tt links a module of static = true "+
			"of the static library it finds of each library, and checks that it needs no "+
			"library of the machine, which a build without tt does not")
	case len(b.config.Bundle) > 0:
		return build.Export{}, build.NoExportf("bundle", "tt copies the shared libraries of "+
			"bundle beside the module, which a build without tt does not")
	}

	script, err := b.exportScript()
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: nil, Lost: b.exportLost()}, nil
}

// The marks of the words of a plan of the script (exportMarks): each stands
// where Run has a value of the machine that builds, and is rendered as the
// variable of sh the script has it in (shellWord).
const (
	markCC          = "\x00CC\x00"
	markCXX         = "\x00CXX\x00"
	markCFLAGS      = "\x00CFLAGS\x00"
	markCXXFLAGS    = "\x00CXXFLAGS\x00"
	markLDFLAGS     = "\x00LDFLAGS\x00"
	markLIBFLAG     = "\x00LIBFLAG\x00"
	markModuleFlags = "\x00TT_MODULE_LDFLAGS\x00"
	markIncludeDir  = "\x00TT_TARANTOOL_INCLUDE_DIR\x00"
	markOutputDir   = "\x00TT_OUTPUT_DIR\x00"
	markModuleExt   = "\x00TT_MODULE_EXT\x00"
	markLinkRoot    = "\x00TT_LINK_ROOT\x00"
	markOrigin      = "\x00origin\x00"
	markPkgCflags   = "\x00pkg_cflags\x00"
	markPkgLibs     = "\x00pkg_libs\x00"
	markLinkLibs    = "\x00link_libs\x00"
	markSource      = "\x00source\x00"
	markObject      = "\x00object\x00"
	markObjects     = "\x00objects\x00"
)

// markText is the text of sh each mark is rendered as: a variable of a list
// of flags unquoted, which the shell splits into words, one of a directory
// in double quotes, a word of it.
var markText = map[string]string{
	markCC: "$CC", markCXX: "$cc_cxx", markCFLAGS: "$CFLAGS", markCXXFLAGS: "$cc_cxxflags",
	markLDFLAGS: "${LDFLAGS-}", markLIBFLAG: "$LIBFLAG", markModuleFlags: "$TT_MODULE_LDFLAGS",
	markIncludeDir: `"$TT_TARANTOOL_INCLUDE_DIR"`, markOutputDir: `"$TT_OUTPUT_DIR"`,
	markModuleExt: `"$TT_MODULE_EXT"`, markLinkRoot: `"$TT_LINK_ROOT"`,
	markOrigin: `"$cc_origin"`, markPkgCflags: "$cc_pkg_cflags", markPkgLibs: "$cc_pkg_libs",
	markLinkLibs: "$cc_link_libs", markSource: `"$1"`, markObject: `"$2"`, markObjects: `"$@"`,
}

// linkIncludeMark is the mark of the -I of include/ of the libraries of the
// component of link: a word of the compiles only when the directory is
// there, as Run gives it then (linkedLibrariesOf).
func linkIncludeMark(component string) string {
	return "\x00include:" + component + "\x00"
}

// linkIncludeVar is the variable of sh the script sets when the component of
// link has include/: cc_include_<component>, its - made _, which no name of
// a component has.
func linkIncludeVar(component string) string {
	return "cc_include_" + strings.ReplaceAll(component, "-", "_")
}

// shellWord renders word, of a plan of the script, as a word of sh: the
// marks it holds as their variables, the rest through build.ShellQuote. -l
// of the mark of the libraries of link, the -l of each of them, is the
// variable that holds them, and the mark of include/ of a component of link
// the -I of it when it is there.
func shellWord(word string) string {
	if word == "-l"+markLinkLibs {
		return markText[markLinkLibs]
	}

	if component, ok := strings.CutPrefix(word, "\x00include:"); ok {
		component = strings.TrimSuffix(component, "\x00")

		return fmt.Sprintf(`${%s+-I"$TT_LINK_ROOT"/%s/include}`, linkIncludeVar(component),
			build.ShellQuote(component))
	}

	var out strings.Builder

	// The parts of word are a literal, a mark, a literal and so on.
	for index, part := range strings.Split(word, "\x00") {
		switch {
		case index%2 == 1:
			out.WriteString(markText["\x00"+part+"\x00"])
		case part != "":
			out.WriteString(build.ShellQuote(part))
		}
	}

	if out.Len() == 0 {
		return build.ShellQuote(word)
	}

	return out.String()
}

// exportToolchain is the toolchain of a plan of the script: the marks of
// the compilers, the flags of a module and the headers of Tarantool, and no
// flag that keeps the paths of the project out of the objects, which Run
// probes.
func exportToolchain() toolchain {
	return toolchain{
		cc: []string{markCC}, cxx: []string{markCXX}, pkgConfig: nil,
		libFlag: []string{markLIBFLAG}, origin: markOrigin, includeDir: markIncludeDir,
		reproducible: nil,
	}
}

// exportEnv are the flags of the environment of a plan of the script: CFLAGS
// and CXXFLAGS set, to their marks, so that the plan has no -O2 -g of Run,
// and LDFLAGS.
func exportEnv() envFlags {
	return envFlags{
		cflagsSet: true, cflags: []string{markCFLAGS}, cxxflagsSet: true,
		cxxflags: []string{markCXXFLAGS}, ldflags: []string{markLDFLAGS},
	}
}

// exportPlan is what the script runs for one OS (exportPart): the words of
// the compile of each language, but the source and the object, those of the
// link, but the objects, the packages of pkg-config and libraries, which the
// libraries of link that it names keep their place among.
type exportPlan struct {
	compileC, compileCXX, link, packages, libraries []string
}

// exportPlanOf returns the plan of the script for goos, of the lists of the
// build and those of platforms.<goos>, by the functions of the plan of Run.
func (b ccBuild) exportPlanOf(goos string) exportPlan {
	config := b.config
	tools := exportToolchain()
	env := exportEnv()
	packages := pkgNames(pkgPackages(config, goos))

	pkg := pkgFlags{cflags: nil, libs: nil}
	if len(packages) > 0 {
		pkg = pkgFlags{cflags: []string{markPkgCflags}, libs: []string{markPkgLibs}}
	}

	linked := linkedLibraries{dirs: nil, includeDirs: nil, names: nil, files: nil}

	includes := make([]string, 0, len(config.Link))

	for _, component := range config.Link {
		linked.dirs = append(linked.dirs, markLinkRoot+"/"+component)
		includes = append(includes, linkIncludeMark(component))
	}

	if len(config.Link) > 0 {
		// Its -l stands for those of the libraries the components made, which
		// the script finds when it runs.
		linked.names = []string{markLinkLibs}
	}

	// The headers of the libraries of link come before those of pkg-config,
	// as in Run.
	compilePkg := pkgFlags{cflags: concat(includes, pkg.cflags), libs: pkg.libs}

	plan := exportPlan{
		compileC: nil, compileCXX: nil, link: nil, packages: packages,
		libraries: concat(config.Libraries, config.Platforms[goos].Libraries),
	}

	compileArgs := []string{"-c", markSource, "-o", markObject}

	for _, lang := range []language{langC, langCXX} {
		if !b.hasLanguage(lang) {
			continue
		}

		compiler := tools.cc
		words := &plan.compileC

		if lang == langCXX {
			compiler, words = tools.cxx, &plan.compileCXX
		}

		*words = slices.Concat(compiler, compileFlags(config, tools, env, compilePkg, goos, lang),
			compileArgs)
	}

	libs := planLibraries(config, goos, "", pkg, linked, nil, false, nil)
	out := markOutputDir + "/" + config.Module + markModuleExt

	// linkArgs begins with LIBFLAG, and has the flag that binds the calls of
	// the module to itself, -Wl,-Bsymbolic-functions of Linux, after it:
	// TT_MODULE_LDFLAGS, which has it on Linux alone.
	args := slices.Insert(linkArgs(config, tools, env, libs, goos, false, false, false,
		[]string{markObjects}, out), len(tools.libFlag), markModuleFlags)

	plan.link = concat(linkerOf(config, tools), args)

	return plan
}

// hasLanguage reports whether a source of the build is of lang, C standing
// for assembler too, which compiles as C does.
func (b ccBuild) hasLanguage(lang language) bool {
	cxx := lang == langCXX

	return slices.ContainsFunc(b.config.Sources, func(source string) bool {
		sourceLang, _ := languageOf(source)

		return (sourceLang == langCXX) == cxx
	})
}

// usesOrigin reports whether an entry of rpath, of the build or of an
// overlay, names the directory of the module.
func (b ccBuild) usesOrigin() bool {
	lists := make([][]string, 0, 1+len(b.config.Platforms))

	lists = append(lists, b.config.Rpath)

	for _, overlay := range b.config.Platforms {
		lists = append(lists, overlay.Rpath)
	}

	return slices.ContainsFunc(lists, func(entries []string) bool {
		return slices.ContainsFunc(entries, originRelative)
	})
}

// usesPkgConfig reports whether the build runs pkg-config on some OS.
func (b ccBuild) usesPkgConfig() bool {
	if len(b.config.PkgConfig) > 0 {
		return true
	}

	for _, overlay := range b.config.Platforms {
		if len(overlay.PkgConfig) > 0 {
			return true
		}
	}

	return false
}

// exportText is export.sh.tmpl, the templates of the script of an export.
//
//go:embed export.sh.tmpl
var exportText string

// exportFuncs are the functions of the templates: q is a string as one word
// of sh (build.ShellQuote), comment a text on one line of a comment
// (comment), command a command of sh broken within exportLineWidth
// (command), and includeVar the variable of include/ of a component of link
// (linkIncludeVar).
var exportFuncs = template.FuncMap{
	"q": build.ShellQuote, "comment": comment, "command": command, "includeVar": linkIncludeVar,
}

// exportTemplate is exportText parsed once, a key the data has not failing
// it.
var exportTemplate = template.Must(template.New("export.sh.tmpl").Funcs(exportFuncs).
	Option("missingkey=error").Parse(exportText))

// renderScript is the template name of export.sh.tmpl executed on data, the
// view the template is written for: it fails on a mistake of the template
// alone.
func renderScript(name string, data any) (string, error) {
	var out strings.Builder

	err := exportTemplate.ExecuteTemplate(&out, name, data)
	if err != nil {
		return "", fmt.Errorf("render the script of the export: %w", err)
	}

	return out.String(), nil
}

// exportLineWidth is the width the script breaks a long command within.
const exportLineWidth = 80

// errCommandWord reports a word of command of a template that is neither a
// string nor a list of them.
var errCommandWord = errors.New("a word of a command is no string")

// command is words, a command of sh, each a word or a list of words, at
// indent, as the template has it on a line of its own: a backslash and a
// newline before a word that would take a line past exportLineWidth, a tab
// counted as 8 columns, and the lines after the first indented once more.
func command(indent string, words ...any) (string, error) {
	flat := make([]string, 0, len(words))

	for _, word := range words {
		switch word := word.(type) {
		case string:
			flat = append(flat, word)
		case []string:
			flat = append(flat, word...)
		default:
			return "", fmt.Errorf("%w: %T", errCommandWord, word)
		}
	}

	width := func(text string) int {
		return len(strings.ReplaceAll(text, "\t", "        "))
	}

	var out strings.Builder

	line := indent

	for index, word := range flat {
		switch {
		case index == 0:
			line += word
		case width(line+" "+word+" \\") > exportLineWidth:
			out.WriteString(line + " \\\n")

			line = indent + "\t" + word
		default:
			line += " " + word
		}
	}

	out.WriteString(line)

	return out.String(), nil
}

// shellWords are words rendered by shellWord, -c and -o each on a line with
// the word after it, which command keeps together.
func shellWords(words []string) []string {
	rendered := make([]string, 0, len(words))

	for index := 0; index < len(words); index++ {
		word := shellWord(words[index])

		if (word == "-c" || word == "-o") && index+1 < len(words) {
			index++

			word += " " + shellWord(words[index])
		}

		rendered = append(rendered, word)
	}

	return rendered
}

// scriptView is what the template script of export.sh.tmpl renders.
type scriptView struct {
	// Module is the module of the block and Backend the name of the backend.
	Module, Backend string
	// C and CXX report whether a source of the build is of the language,
	// C standing for assembler too (hasLanguage), PkgConfig whether the
	// build runs pkg-config on some OS (usesPkgConfig), and Origin whether
	// its rpath names the directory of the module (usesOrigin).
	C, CXX, PkgConfig, Origin bool
	// Link are the components of link.
	Link []string
	// Arms are the OSes whose lists of platforms.<os> make another part
	// than those of the build alone, Base, each of its part, and Other the
	// part of the other OSes, when there are any.
	Arms  []armView
	Other *partView
	Base  partView
	// Sources are the compiles of the sources, in their order, and Objects
	// the objects they make, which the link takes.
	Sources []sourceView
	Objects []string
}

// armView is the part of the script of the OS OS, an arm of the case on
// TT_PLATFORM_OS.
type armView struct {
	OS   string
	Part partView
}

// partView is what the template part renders, the plan of an OS
// (exportPlanOf) at Indent.
type partView struct {
	Indent string
	// Packages are the packages pkg-config is run for.
	Packages []string
	// Link are the components of link, and Listed the libraries of the
	// plan, each between two blanks, which cc_link_libraries adds none of.
	Link   []string
	Listed string
	// CompileC, CompileCXX and LinkWords are the words of sh of the compile
	// of a source of each language, empty for a language of no source, and
	// of the link (shellWords).
	CompileC, CompileCXX, LinkWords []string
}

// sourceView is a source of the build, Path, of C++ or not (CXX), and
// Object, the word of sh of its object in TT_BUILD_DIR.
type sourceView struct {
	Path   string
	CXX    bool
	Object string
}

// exportScript is the script of the export (Export): what the script runs
// for the OS it builds on is the part of the lists of the build alone, and
// a case on TT_PLATFORM_OS when an OS has another, of the lists of its
// platforms.<os>, each in its arm, the part of the build alone in the arm
// of the other OSes, when there are any.
func (b ccBuild) exportScript() (string, error) {
	basePart := b.exportPart("", "")

	base, err := renderScript("part", basePart)
	if err != nil {
		return "", err
	}

	var arms []armView

	for _, goos := range build.SupportedOS() {
		part, err := renderScript("part", b.exportPart(goos, ""))
		if err != nil {
			return "", err
		}

		if part != base {
			arms = append(arms, armView{OS: goos, Part: b.exportPart(goos, "\t")})
		}
	}

	var other *partView

	if len(arms) > 0 && len(arms) < len(build.SupportedOS()) {
		part := b.exportPart("", "\t")

		other = &part
	}

	sources := make([]sourceView, 0, len(b.config.Sources))
	objects := make([]string, 0, len(b.config.Sources))

	for _, source := range b.config.Sources {
		lang, _ := languageOf(source)
		object := `"$TT_BUILD_DIR"/` + build.ShellQuote(objectName(source))

		sources = append(sources, sourceView{Path: source, CXX: lang == langCXX, Object: object})
		objects = append(objects, object)
	}

	return renderScript("script", scriptView{
		Module: b.config.Module, Backend: b.name, C: b.hasLanguage(langC),
		CXX: b.hasLanguage(langCXX), PkgConfig: b.usesPkgConfig(), Origin: b.usesOrigin(),
		Link: b.config.Link, Arms: arms, Other: other, Base: basePart,
		Sources: sources, Objects: objects,
	})
}

// exportPart is the part of the script for goos, of its plan (exportPlanOf),
// at indent.
func (b ccBuild) exportPart(goos, indent string) partView {
	plan := b.exportPlanOf(goos)

	// The names, each between two blanks.
	listed := " "
	if len(plan.libraries) > 0 {
		listed = " " + strings.Join(plan.libraries, " ") + " "
	}

	return partView{
		Indent: indent, Packages: plan.packages, Link: b.config.Link, Listed: listed,
		CompileC: shellWords(plan.compileC), CompileCXX: shellWords(plan.compileCXX),
		LinkWords: shellWords(plan.link),
	}
}

// exportLost is what the script lacks against Run (build.Export.Lost), a
// phrase each, as short as the warning of the pack, which names the
// component before each, is to stay.
func (b ccBuild) exportLost() []string {
	checks := "the checks of the module it links: of the symbols it exports that " +
		"Tarantool exports too, and of its load into Tarantool"
	if b.name == LuaCName {
		checks = "the checks of the module it links: of its entry point, of the symbols it " +
			"exports that Tarantool exports too, and of its load into Tarantool"
	}

	var flags []string

	if b.hasLanguage(langC) {
		flags = append(flags, "C compiles with CFLAGS of LuaRocks, -O2 -fPIC by default")
	}

	switch {
	case !b.hasLanguage(langCXX):
	case len(flags) > 0:
		flags = append(flags, "C++ with CXXFLAGS, -O2 by default")
	default:
		flags = append(flags, "C++ compiles with CXXFLAGS, -O2 by default")
	}

	return []string{
		"the cache of its objects and compile_commands.json",
		checks,
		"-g of the default -O2 -g: " + strings.Join(flags, ", "),
		"the flags that keep the paths of the project out of the objects",
	}
}

// comment is text on one line of a comment of the script.
func comment(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
