package build

import (
	"errors"
	"fmt"
	"strings"
)

// Exporter is a Build that is made without tt too: by a script of POSIX sh
// that makes what Run makes. tt package pack asks every component of the
// product it packs and every hook a build runs (pre_build, post_build) for
// its script, and writes the scripts into the rockspec of the rock of the
// package, so that LuaRocks - tt rocks of tt 2, a LuaRocks configured for
// Tarantool - builds the source rock of the package with no tt. Export is
// all or nothing for a package: a component or a hook that is no Exporter,
// or whose Export fails with [ErrNoExport], makes the rockspec fail under
// LuaRocks, saying which, and the pack warn of it.
//
// A backend whose builds are Exporters says so ([Backend.Exports]), and the
// core refuses a Build of it that is none, and an Exporter of a backend that
// does not say so ([Backend.ValidateBuild]). It finds an Exporter by a type
// assertion; a method Export of another signature of a backend that says so
// is refused too, rather than never called, and one of a backend that does
// not is the backend's own. buildtest.CheckExport runs Run and the script on
// two copies of one project and compares what they made, so that a script
// that makes something else fails the tests of its backend.
type Exporter interface {
	Export(e *ExportContext) (Export, error)
}

// ExportContext is what [Exporter.Export] is given: what is known of a build
// when the package is packed, rather than where it is built. What only the
// machine that builds it knows - its directories, its platform, its
// Tarantool and its toolchain - the script reads from its variables
// ([Export]). What a later SDK tells Export goes into it, so that the
// signature of Export stays.
type ExportContext struct {
	// Component is the component built, as [Context.Component] has it; the
	// zero one for a hook.
	Component Component
	// Hook is set for the export of a lifecycle hook, as [Context.Hook]: its
	// script runs in the working directory of the hook, without the
	// variables of a component.
	Hook bool
	// LockedVersion returns the version of the rock the lock the package is
	// packed with pins, and whether it pins one, as [Context.LockedVersion]
	// does: for a script that pins a tool to the version of a rock, which it
	// writes in as a literal. LuaRocks resolves the dependencies of the
	// rockspec, not the lock, so the rock it installs may be another version.
	// The core and buildtest give one always.
	LockedVersion func(rock string) (string, bool)
}

// Export is how a build is made without tt ([Exporter]).
//
// The core runs Script with set -eu, in a subshell whose working directory
// is that of the build ([Dirs.Cwd]), after the hook pre_build and the
// builds of the components before it in the product, as tt runs Run. It puts
// what it makes where Run does: modules into "$TT_OUTPUT_DIR", data files
// into "$TT_SHARE_DIR", programs into "$TT_PROGRAMS_DIR" and libraries for
// linking into "$TT_LINK_DIR". For a backend of [OutputCwd] the core copies
// the files output lists after the script, as it does after Run; one of
// [OutputBackend] copies them in its script.
//
// The variables of the script are those of a build of tt, by the same names
// and of the same meaning, and a few of the export alone:
//
//   - TT_PROJECT_ROOT, TT_PACKAGE and TT_VERSION; TT_PLATFORM_OS and
//     TT_PLATFORM_ARCH, the machine that builds, as uname names it and tt
//     names a platform (linux, darwin; amd64, arm64); TT_BUILD_JOBS, one a
//     CPU;
//   - TT_OUTPUT_DIR, TT_COMPONENT_NAME and TT_BUILD_DIR, which a hook has
//     not, as in tt; TT_BUILD_DIR is empty when the build begins;
//   - TT_TARANTOOL_INCLUDE_DIR, the headers of Tarantool: LUA_INCDIR of
//     LuaRocks, set when module.h is there; TT_TARANTOOL_PREFIX, the prefix
//     of LUA_INCDIR when it ends with /include/tarantool, and unset
//     otherwise;
//   - of the export alone, for a component: TT_SHARE_DIR ([Dirs.Share]),
//     TT_PROGRAMS_DIR ([Dirs.Programs]) and TT_LINK_DIR ([Dirs.Link]), each
//     created before the script runs;
//   - of the export alone, for every build: TT_STATE_DIR ([Dirs.State]);
//     TT_LINK_ROOT, the directory of the TT_LINK_DIR of every component, so
//     that "$TT_LINK_ROOT/<component>" is [Dirs.LinkOf]; TT_MODULE_EXT, the
//     extension of a module ([Target.ModuleExt]); TT_MODULE_LDFLAGS, what
//     tt links a module with besides LIBFLAG: -Wl,-Bsymbolic-functions on
//     Linux, nothing on macOS;
//   - the toolchain of LuaRocks: CC, CFLAGS and LIBFLAG, the flags that link
//     a module (-shared; -bundle -undefined dynamic_lookup -all_load on
//     macOS), and CXX, CXXFLAGS and LDFLAGS when the environment has them;
//   - the env of the build block, which the variables above win over, as in
//     tt, and the environment LuaRocks runs the build in.
//
// TT_TARANTOOL_PREFIX and the toolchain of the environment may be unset, so
// a script reads them as "${CXX-}" under set -u. A variable a script keeps a
// value of its own in is named tt_<backend>_<name>: the core keeps its own
// in variables of tt_, and refuses the env of a block that sets one of tt_
// or cc_, which the scripts of the standard backends name theirs with.
type Export struct {
	// Script is the script of POSIX sh that makes what Run makes. A literal
	// of the build block goes into it through ShellQuote, so that it is the
	// word it was whatever it holds. The core writes it in as it is, without
	// indenting its lines, so a here-document of it ends as it would alone.
	Script string
	// Tools are the programs the script runs, which the core looks for in
	// PATH with command -v before the first build runs, failing the build
	// when one is not there, as Preflight fails one of tt.
	Tools []string
	// Lost are what a build by the script lacks against Run, a sentence
	// each: the pack warns of them once, and writes them into the script as
	// comments.
	Lost []string
}

// ErrNoExport reports a build block that is made with tt alone: a field of
// it, or the value the field has, has no counterpart in a script - a check
// the backend makes of what it built, say. Export returns an error wrapping
// it and a [FieldError] naming the field ([NoExportf]); the pack warns of
// the build, and the rockspec of the package fails under LuaRocks, saying
// so.
var ErrNoExport = errors.New("the build is made with tt alone")

// NoExportf returns the error of an Export of a build whose field, named as
// a [FieldError] names it, has no counterpart in a script: one that wraps
// [ErrNoExport] and the FieldError of the field, with a message formatted as
// fmt.Sprintf formats it.
func NoExportf(field, format string, a ...any) error {
	return fmt.Errorf("%w: %w", ErrNoExport, FieldErrorf(field, format, a...))
}

// shellSafe are the bytes a word of POSIX sh may have unquoted, whatever
// command it is a word of: no byte the shell gives a meaning, = included,
// which makes the first word of a command an assignment, and ~, which a
// word begins a path of a home directory with.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+:,./-"

// ShellQuote quotes s as one word of POSIX sh, which the shell reads as s
// wherever the word is: s as it is when it is not empty and every byte of it
// is a letter, a digit or one of _@%+:,./-, and s in single quotes
// otherwise, which hold any byte but a single quote: that one closes them,
// is escaped and opens them again. A $, a `, a backslash and a newline in
// single quotes are themselves.
//
//	ShellQuote("make")    // make
//	ShellQuote("V=1")     // 'V=1'
//	ShellQuote("it's $x") // 'it'\''s $x'
func ShellQuote(s string) string {
	if s != "" && strings.Trim(s, shellSafe) == "" {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
