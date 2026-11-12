package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	lrbuild "github.com/tarantool/go-luarocks/build"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// initialArgsCap pre-sizes the cc argument slice to avoid early regrows.
const initialArgsCap = 16

// defaultCFLAGS is the optimization a build compiles with when CFLAGS of the
// environment is unset, as LuaRocks does.
const defaultCFLAGS = "-O2"

// ccBackend is the shared cc driver for the c and lua-c backends. Both compile
// build.Sources into one <module><Ext> shared library with tt's flags (the
// per-OS link flags of the injected lrbuild.Flags, -fPIC and the headers of
// Tarantool), the component's flags, defines / include dirs / library dirs /
// libraries and the per-OS overlay, and the flags of the user's environment.
// The two backends differ only in how the artifact is later loaded (require
// vs box.schema.func.create), which is not this driver's concern.
type ccBackend struct {
	// flags is the compile/link toolchain derived once from the rocks adapter.
	// Its CFLAGS and LDFLAGS are not taken: they mix tt's defaults with the
	// environment, and the environment goes last (see envFlags).
	flags lrbuild.Flags
	// showOutput streams child output when true.
	showOutput bool
}

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
//	$CC [-O2 unless CFLAGS is set] -fPIC <cflags...> <$CFLAGS...> \
//	    -I<Tarantool incdir> -D<define...> -I<incdir...> <LIBFLAG...> \
//	    <ldflags...> -o <out> <sources...> \
//	    {-L<libdir> [-Wl,-rpath,<libdir> if GccRpath]}... -l<lib...> <$LDFLAGS...>
//
// The flags of the compiler come in three parts: tt's defaults, the
// project's (cflags) and the user's (CFLAGS of the environment), the last
// winning a conflict, as CFLAGS comes after AM_CFLAGS in autotools: CFLAGS
// of "-O0 -g" builds for a debugger, -Wno-error undoes the project's
// -Werror. The project's ldflags go before the sources, as make's
// $(CC) $(LDFLAGS) -o out objs $(LDLIBS) has them, so that an option like
// -Wl,--as-needed acts on the libraries that follow; LDFLAGS of the
// environment ends the line, as go-luarocks has it. The rest mirrors the
// go-luarocks builtin compileModule.
//
// cflags / ldflags / defines / include dirs / library dirs / libraries come
// from build, each merged (append-only) with the build.Platforms[goos]
// overlay; library order is preserved because link order matters. Each -L is
// immediately followed by its matching -Wl,-rpath, entry when flags.GccRpath
// is set (interleaved per libdir). out is the fully resolved artifact path;
// sources are emitted verbatim and resolved against cwd by the child
// (cmd.Dir=cwd). It is a pure function so the argv is testable without a
// compiler or Tarantool headers.
func ccArgs(build manifest.Build, flags lrbuild.Flags, env envFlags, out, goos string) []string {
	overlay := build.Platforms[goos]

	defines := concat(build.Defines, overlay.Defines)
	includeDirs := concat(build.IncludeDirs, overlay.IncludeDirs)
	libraryDirs := concat(build.LibraryDirs, overlay.LibraryDirs)
	libraries := concat(build.Libraries, overlay.Libraries)

	args := make([]string, 0, initialArgsCap)

	if !env.cflagsSet {
		args = append(args, defaultCFLAGS)
	}

	args = append(args, "-fPIC")
	args = append(args, build.Cflags...)
	args = append(args, overlay.Cflags...)
	args = append(args, env.cflags...)

	if flags.LuaIncDir != "" {
		args = append(args, "-I"+flags.LuaIncDir)
	}

	for _, d := range defines {
		args = append(args, "-D"+d)
	}

	for _, inc := range includeDirs {
		args = append(args, "-I"+inc)
	}

	args = append(args, flags.LIBFLAG...)
	args = append(args, build.Ldflags...)
	args = append(args, overlay.Ldflags...)
	args = append(args, "-o", out)
	args = append(args, build.Sources...)

	for _, dir := range libraryDirs {
		args = append(args, "-L"+dir)
		if flags.GccRpath {
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

const (
	// linuxModuleExt is the extension of a native module on Linux.
	linuxModuleExt = ".so"
	// darwinModuleExt is the extension of a native module on macOS: Tarantool
	// looks for .rocks/lib/tarantool/?.dylib there, and for no .so.
	darwinModuleExt = ".dylib"
)

// moduleExt is the extension of a native module Tarantool loads on goos:
// .dylib on macOS, and the extension of the toolchain, .so, elsewhere. The
// toolchain says .so on macOS too, as LuaRocks does, which Tarantool would
// not find.
func moduleExt(goos, ext string) string {
	if goos == darwinOS {
		return darwinModuleExt
	}

	return ext
}

// artifactName builds the compiled artifact's flat leaf filename from the
// module name and the toolchain extension. module is used verbatim (a dotted
// module yields a literal a.b<ext>, not slashed into subdirectories) and the
// extension comes from flags.Ext, never a hardcoded ".so".
func artifactName(module, ext string) string {
	return module + ext
}

// Run compiles the c / lua-c component. It asserts the absolute-path contract,
// fails fast when the Tarantool headers are unconfigured, creates OutputDir,
// then runs one cc invocation producing <module><ext> directly in OutputDir,
// the extension being the one Tarantool loads on the target (see moduleExt).
// module is a flat leaf name, so a dotted module yields a literal a.b.so — it
// is not slashed into subdirectories. c / lua-c ignore build.Output (the artifact
// is the compiled library, not a copy list).
func (c ccBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	if c.flags.LuaIncDir == "" {
		return ErrMissingHeaders
	}

	err = os.MkdirAll(env.OutputDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory %q: %w", env.OutputDir, err)
	}

	ext := moduleExt(env.platformOS(), c.flags.Ext)
	out := filepath.Join(env.OutputDir, artifactName(build.Module, ext))
	args := ccArgs(build, c.flags, environFlags(os.LookupEnv), out, env.platformOS())

	return run(ctx, cwd, env, c.showOutput, c.flags.CC, args...)
}
