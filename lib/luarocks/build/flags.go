// Package build implements the four supported rockspec build backends —
// builtin, cmake, make, command — plus the none no-op. RunBackend is the
// single public entry point used by the facade.
//
// The package never sets process environment variables. Every
// subprocess invocation builds its env via cmd.Env, layering on top of
// os.Environ() with the five canonical TARANTOOL_DIR / LUA_* vars and any
// rockspec-supplied K=V pairs.
//
// All subprocesses receive ctx via exec.CommandContext. Output
// shared-library extension is `.so` on both linux and macOS (upstream
// luarocks sets `lib_extension = "so"` for unix unconditionally,
// even on macOS where the linker emits a -bundle).
package build

import (
	"os"
	"path/filepath"
	"runtime"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// Flags is the resolved compile/link toolchain configuration for a single
// build invocation. The fields are derived from rocks.Config plus the host
// GOOS; the build backends consume them when constructing cc / ld command
// lines.
//
// CFLAGS always contains "-fPIC" on unix and "-I<IncludeDir>" iff
// cfg.Tarantool.IncludeDir is set. CFLAGS does NOT inject "-llua" or
// "-lluajit"; for Tarantool the Lua symbols are resolved at dlopen against
// the running tarantool executable.
//
// LIBFLAG carries the shared-library link flag list:
//
//   - linux: ["-shared"]
//   - macOS: ["-bundle", "-undefined", "dynamic_lookup", "-all_load"]
//
// Ext is always ".so" on unix (per upstream cfg.lib_extension = "so").
type Flags struct {
	// CC is the C compiler to invoke (the CC env var, else the platform default).
	CC string
	// CFLAGS are the compile flags (see the type doc for how they are built).
	CFLAGS []string
	// LDFLAGS is the extra-link-flags slot consumed on the cc line. DeriveFlags
	// does not populate it (it is currently always empty); it exists so callers
	// and future config can supply additional link flags without a signature
	// change.
	LDFLAGS []string
	// LIBFLAG is the per-OS shared-library link flag list (see the type doc).
	LIBFLAG []string
	// LuaIncDir is the Lua/Tarantool include dir (empty when none is configured).
	LuaIncDir string
	// LuaLibDir is the Lua/Tarantool library dir, when configured.
	LuaLibDir string
	// LuaBinDir is the Lua/Tarantool bin dir, when configured.
	LuaBinDir string
	// Ext is the compiled-module extension — always ".so" on unix.
	Ext string
}

// DeriveFlags resolves toolchain Flags for the running host. The result is
// pure data — no env writes, no subprocesses — so it is safe to call from
// tests on any platform.
//
// CC is taken from the CC environment variable if set, otherwise the
// platform default ("cc"). DeriveFlags itself reads CC via os.Getenv but
// never writes process env.
//
// If cfg.Tarantool.IncludeDir is empty the returned CFLAGS omits the
// "-I" entry and LuaIncDir is empty. Backends that require headers detect
// this case themselves and return ErrMissingTarantoolHeaders.
func DeriveFlags(cfg rocks.Config) Flags {
	return deriveFlagsFor(cfg, runtime.GOOS)
}

// deriveFlagsFor is the GOOS-injected helper that backs DeriveFlags. Tests
// invoke it directly to cover both "linux" and "darwin" without depending
// on the host runtime.GOOS.
func deriveFlagsFor(cfg rocks.Config, goos string) Flags {
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	f := Flags{
		CC:        cc,
		LuaIncDir: cfg.Tarantool.IncludeDir,
		Ext:       ".so",
	}

	// CFLAGS: always -fPIC on unix; -I<incdir> only if we have one.
	f.CFLAGS = append(f.CFLAGS, "-fPIC")
	if cfg.Tarantool.IncludeDir != "" {
		f.CFLAGS = append(f.CFLAGS, "-I"+cfg.Tarantool.IncludeDir)
	}

	switch goos {
	case "darwin":
		// -bundle (NOT -dynamiclib) so the loader resolves symbols against
		// the host executable at dlopen time. No -Wl,-rpath on macOS —
		// upstream luarocks defaults gcc_rpath=false there.
		f.LIBFLAG = []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"}
	default:
		// Treat anything non-darwin as linux/unix for our purposes; this
		// package is unix-only and the dispatcher does not gate on
		// GOOS otherwise.
		f.LIBFLAG = []string{"-shared"}
		// gcc_rpath: hardcoded false for now (no Config flag exposed).
		// When we add the toggle, append "-Wl,-rpath=" + LuaLibDir here.
	}

	if cfg.Tarantool.Prefix != "" {
		f.LuaLibDir = filepath.Join(cfg.Tarantool.Prefix, "lib")
		f.LuaBinDir = filepath.Join(cfg.Tarantool.Prefix, "bin")
	}

	return f
}
