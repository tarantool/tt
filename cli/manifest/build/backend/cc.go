package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	lrbuild "github.com/tarantool/go-luarocks/build"

	"github.com/tarantool/tt/cli/manifest"
)

// initialArgsCap pre-sizes the cc argument slice to avoid early regrows.
const initialArgsCap = 16

// ccBackend is the shared cc driver for the c and lua-c backends. Both compile
// b.Sources into one <module><Ext> shared library with the base per-OS flags
// (from the injected lrbuild.Flags) plus the component's defines / include dirs
// / library dirs / libraries and the per-OS overlay. The two backends differ
// only in how the artifact is later loaded (require vs box.schema.func.create),
// which is not this driver's concern.
type ccBackend struct {
	// flags is the compile/link toolchain derived once from the rocks adapter.
	flags lrbuild.Flags
	// showOutput streams child output when true.
	showOutput bool
}

// ccArgs builds the cc argv, mirroring the go-luarocks builtin compileModule
// token for token:
//
//	$CC <CFLAGS...> -D<define...> -I<incdir...> <LIBFLAG...> -o <out> \
//	    <sources...> {-L<libdir> [-Wl,-rpath,<libdir> if GccRpath]}... \
//	    -l<lib...> <LDFLAGS...>
//
// defines / include dirs / library dirs / libraries come from b, each merged
// (append-only) with the b.Platforms[goos] overlay; library order is preserved
// because link order matters. Each -L is immediately followed by its matching
// -Wl,-rpath, entry when flags.GccRpath is set (interleaved per libdir). out is
// the fully resolved artifact path; sources are emitted verbatim and resolved
// against cwd by the child (cmd.Dir=cwd). It is a pure function so the argv is
// testable without a compiler or Tarantool headers.
func ccArgs(b manifest.Build, flags lrbuild.Flags, out, goos string) []string {
	overlay := b.Platforms[goos]

	defines := concat(b.Defines, overlay.Defines)
	includeDirs := concat(b.IncludeDirs, overlay.IncludeDirs)
	libraryDirs := concat(b.LibraryDirs, overlay.LibraryDirs)
	libraries := concat(b.Libraries, overlay.Libraries)

	args := make([]string, 0, initialArgsCap)
	args = append(args, flags.CFLAGS...)

	for _, d := range defines {
		args = append(args, "-D"+d)
	}

	for _, inc := range includeDirs {
		args = append(args, "-I"+inc)
	}

	args = append(args, flags.LIBFLAG...)
	args = append(args, "-o", out)
	args = append(args, b.Sources...)

	for _, dir := range libraryDirs {
		args = append(args, "-L"+dir)
		if flags.GccRpath {
			args = append(args, "-Wl,-rpath,"+dir)
		}
	}

	for _, lib := range libraries {
		args = append(args, "-l"+lib)
	}

	return append(args, flags.LDFLAGS...)
}

// concat returns base followed by extra in a fresh slice (an append-only
// overlay merge that never mutates either input).
func concat(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)

	return append(out, extra...)
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
// then runs one cc invocation producing <module><Ext> directly in OutputDir.
// module is a flat leaf name, so a dotted module yields a literal a.b.so — it
// is not slashed into subdirectories. c / lua-c ignore b.Output (the artifact
// is the compiled library, not a copy list).
func (c ccBackend) Run(ctx context.Context, b manifest.Build, cwd string, env Env) error {
	if err := requireAbsPaths(cwd, env.OutputDir); err != nil {
		return err
	}

	if c.flags.LuaIncDir == "" {
		return ErrMissingHeaders
	}

	if err := os.MkdirAll(env.OutputDir, dirPerm); err != nil {
		return fmt.Errorf("create output directory %q: %w", env.OutputDir, err)
	}

	out := filepath.Join(env.OutputDir, artifactName(b.Module, c.flags.Ext))
	args := ccArgs(b, c.flags, out, env.platformOS())

	return run(ctx, cwd, env, c.showOutput, c.flags.CC, args...)
}
