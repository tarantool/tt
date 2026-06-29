package client

// luaEngine is the gopher-lua backend (BackendLua). It boots the embedded
// Tarantool LuaRocks fork (3.9.2) into a single, long-lived gopher-lua VM and
// dispatches write operations through extra/wrapper.lua's exec() entry point.
//
// Environment-override policy:
//
// The engine installs a custom os.getenv into the VM that consults an
// envOverride map first and falls through to the host process env for any
// other key. The override map serves only the four LuaRocks build-config keys
// consumed by extra/hardcoded.lua — LUAROCKS_PREFIX, LUA_INCDIR, LUA_BINDIR,
// TARANTOOL_DIR. Every other os.getenv read (CC, CFLAGS, LDFLAGS, AR, RANLIB,
// LINK, MT, MAKE, WINDRES, CMAKE_*, PATH, HOME, USER, TMPDIR/TMP/TEMP, XDG_*,
// http_proxy/https_proxy/no_proxy, LUAROCKS_SYSCONFDIR,
// LUAROCKS_CROSS_COMPILING, LUA_PATH_/LUA_CPATH_, and Windows-only vars) falls
// through to the host process env by design — the build toolchain genuinely
// lives there. The engine never mutates the host env: it never calls
// os.Setenv.
//
// Boot is lazy: newLuaEngine does not touch the VM; the LState is created
// and populated on the first call() via bootOnce. The LState is single-threaded:
// concurrent call()s serialize through e.mu. Only Lua that arrived via
// the embedded FS is executed — embedded module bytes via LoadString plus the
// fixed wrapper dispatch string via DoString; no caller-supplied Lua source and
// no loadfile/dofile exposure.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"
	rocks "github.com/tarantool/tt/lib/luarocks"
	luarocksembed "github.com/tarantool/tt/lib/luarocks/internal/luarocks"
)

// osExitSentinel prefixes the Lua error raised by the engine's os.exit
// replacement so call() can recover the requested exit code from the error
// message produced by DoString.
const osExitSentinel = "go-luarocks os.exit: "

const (
	// luaConfigFileMode is the permission for the generated LUAROCKS_CONFIG
	// file: owner read/write only (it holds no secrets but needs no wider access).
	luaConfigFileMode = 0o600

	// ioOpenModeArgIndex is the 1-based Lua argument position of io.open's mode
	// string; ioOpenMinArgsForMode is the argument count at or above which a
	// mode string is present (io.open(filename, mode)).
	ioOpenMinArgsForMode = 2

	// printWriteArgCount is the number of values pushed for the io.stdout write
	// call in the print shim (the file handle plus the line string).
	printWriteArgCount = 2

	// searchFieldsMin is the minimum tab-separated field count a --porcelain
	// search line must have to be a result record (name, version, arch, repo).
	searchFieldsMin = 4

	// decimalBase is the radix used when accumulating the exit-code digits.
	decimalBase = 10

	// globalArgsTreePair counts the two argv entries the mandatory --tree global
	// always contributes; per-server entries add two more apiece.
	globalArgsTreePair = 2
	argsPerServer      = 2
)

// luaEngine implements the Engine interface against an embedded LuaRocks VM.
type luaEngine struct {
	cfg         rocks.Config
	store       rocks.ManifestStore
	logger      *slog.Logger
	envOverride map[string]string

	lstate   *lua.LState
	bootOnce sync.Once
	bootErr  error
	mu       sync.Mutex

	// configDir is the temp dir holding the generated LUAROCKS_CONFIG file
	// (set by writeConfigFile during boot). It must outlive boot since cfg.lua
	// is read on first require; a finalizer (set in newLuaEngine) removes it and
	// closes the LState when the engine becomes unreachable — best-effort, since
	// the engine has no explicit Close in the public API.
	configDir string

	// callImpl, when non-nil, replaces callViaState as the dispatch backend.
	// It exists solely as a test seam so dispatch tests can assert the exact
	// argv a method builds (and feed a canned stdout to data-returning
	// methods) without booting the embedded VM. Production code leaves it nil.
	callImpl func(argv []string) (string, error)
}

// newLuaEngine constructs the engine and populates the env-override map from
// cfg.Tarantool. It does NOT boot the LState — boot is lazy on first
// call().
func newLuaEngine(cfg rocks.Config, store rocks.ManifestStore, logger *slog.Logger) *luaEngine {
	envOverride := map[string]string{}
	// Only add an entry when the source value is non-empty so the host-env
	// fallback can still apply for keys we have no opinion on.
	if cfg.Tarantool.Prefix != "" {
		envOverride["LUAROCKS_PREFIX"] = cfg.Tarantool.Prefix
		envOverride["LUA_BINDIR"] = filepath.Join(cfg.Tarantool.Prefix, "bin")
	}

	if cfg.Tarantool.IncludeDir != "" {
		envOverride["LUA_INCDIR"] = cfg.Tarantool.IncludeDir
		envOverride["TARANTOOL_DIR"] = filepath.Dir(cfg.Tarantool.IncludeDir)
	}

	if cfg.Tarantool.Version != "" {
		// The tarantool/luarocks fork registers `tarantool` as a provided
		// dependency from this env var (util.lua add_provided_versions) — there
		// is no `_TARANTOOL` global in the gopher-lua VM, so the env is the only
		// source. Without it, rockspecs with `dependencies = {"tarantool >= X"}`
		// fail resolution. Mirrors tt's TT_CLI_TARANTOOL_VERSION.
		envOverride["TT_CLI_TARANTOOL_VERSION"] = cfg.Tarantool.Version
	}

	e := &luaEngine{
		cfg:         cfg,
		store:       store,
		logger:      logger,
		envOverride: envOverride,
	}
	// Best-effort cleanup of the temp config dir and the cached LState when the
	// engine is garbage-collected. The engine has no public Close (adding one
	// would burden every caller); a finalizer keeps New(...WithBackend(BackendLua))
	// from leaking a temp dir per instance (e.g. across test runs).
	runtime.SetFinalizer(e, func(e *luaEngine) { e.cleanup() })

	return e
}

// cleanup removes the generated config dir and closes the cached LState. Safe
// to call when neither was created (empty configDir, nil lstate).
func (e *luaEngine) cleanup() {
	if e.configDir != "" {
		_ = os.RemoveAll(e.configDir)
	}

	if e.lstate != nil {
		e.lstate.Close()
	}
}

// luaPreloadMap maps gopher-lua require names to their embedded-FS paths.
// Mirrors tt/cli/rocks/rocks.go's rocks_preload, with the path prefixes adapted
// to this module's embed layout: upstream modules live under "src/src/" (the
// git subtree placed the upstream repo root at internal/luarocks/src/), shims
// under "extra/". macosx maps to the upstream module — we ship no extra shim.
var luaPreloadMap = func() map[string]string {
	rocksPath := "src/src/"
	extraPath := "extra/"

	return map[string]string{
		"extra.wrapper":           extraPath + "wrapper.lua",
		"luarocks.core.hardcoded": extraPath + "hardcoded.lua",
		"luarocks.core.util":      rocksPath + "luarocks/core/util.lua",
		"luarocks.core.persist":   rocksPath + "luarocks/core/persist.lua",
		"luarocks.core.sysdetect": rocksPath + "luarocks/core/sysdetect.lua",
		"luarocks.core.cfg":       rocksPath + "luarocks/core/cfg.lua",
		"luarocks.core.dir":       rocksPath + "luarocks/core/dir.lua",
		"luarocks.core.path":      rocksPath + "luarocks/core/path.lua",
		"luarocks.core.manif":     rocksPath + "luarocks/core/manif.lua",
		"luarocks.core.vers":      rocksPath + "luarocks/core/vers.lua",
		"luarocks.util":           rocksPath + "luarocks/util.lua",
		"luarocks.loader":         rocksPath + "luarocks/loader.lua",
		"luarocks.dir":            rocksPath + "luarocks/dir.lua",
		"luarocks.path":           rocksPath + "luarocks/path.lua",
		"luarocks.fs":             rocksPath + "luarocks/fs.lua",
		"luarocks.persist":        rocksPath + "luarocks/persist.lua",
		"luarocks.fun":            rocksPath + "luarocks/fun.lua",
		"luarocks.tools.patch":    rocksPath + "luarocks/tools/patch.lua",
		"luarocks.tools.zip":      rocksPath + "luarocks/tools/zip.lua",
		"luarocks.tools.tar":      rocksPath + "luarocks/tools/tar.lua",
		"luarocks.fs.unix":        rocksPath + "luarocks/fs/unix.lua",
		// luarocks.fs.macosx is INTENTIONALLY NOT preloaded. Its is_dir/is_file
		// (internal/luarocks/.../fs/macosx.lua) probe directory-ness via
		// io.open(path.."/.", "r") and inspect the PUC-Lua errno returned as the
		// third result (codes 2/13/20/21). gopher-lua's io.open does not surface
		// libc errno that way, so macosx.is_dir returns false for EVERY existing
		// directory — which breaks fs.check_command_permissions (the tree always
		// looks unwritable) and every downstream mkdir/copy. Omitting the module
		// makes require("luarocks.fs.macosx") fail in load_platform_fns's pcall,
		// so fs.init falls through to the shell-out tools backend
		// (luarocks.fs.unix.tools → `test -d`), which works correctly in-VM.
		"luarocks.fs.unix.tools":           rocksPath + "luarocks/fs/unix/tools.lua",
		"luarocks.fs.lua":                  rocksPath + "luarocks/fs/lua.lua",
		"luarocks.fs.tools":                rocksPath + "luarocks/fs/tools.lua",
		"luarocks.queries":                 rocksPath + "luarocks/queries.lua",
		"luarocks.type_check":              rocksPath + "luarocks/type_check.lua",
		"luarocks.type.rockspec":           rocksPath + "luarocks/type/rockspec.lua",
		"luarocks.rockspecs":               rocksPath + "luarocks/rockspecs.lua",
		"luarocks.signing":                 rocksPath + "luarocks/signing.lua",
		"luarocks.fetch":                   rocksPath + "luarocks/fetch.lua",
		"luarocks.type.manifest":           rocksPath + "luarocks/type/manifest.lua",
		"luarocks.manif":                   rocksPath + "luarocks/manif.lua",
		"luarocks.build.builtin":           rocksPath + "luarocks/build/builtin.lua",
		"luarocks.deps":                    rocksPath + "luarocks/deps.lua",
		"luarocks.deplocks":                rocksPath + "luarocks/deplocks.lua",
		"luarocks.cmd":                     rocksPath + "luarocks/cmd.lua",
		"luarocks.argparse":                rocksPath + "luarocks/argparse.lua",
		"luarocks.test.busted":             rocksPath + "luarocks/test/busted.lua",
		"luarocks.test.command":            rocksPath + "luarocks/test/command.lua",
		"luarocks.results":                 rocksPath + "luarocks/results.lua",
		"luarocks.search":                  rocksPath + "luarocks/search.lua",
		"luarocks.repos":                   rocksPath + "luarocks/repos.lua",
		"luarocks.cmd.show":                rocksPath + "luarocks/cmd/show.lua",
		"luarocks.cmd.path":                rocksPath + "luarocks/cmd/path.lua",
		"luarocks.cmd.write_rockspec":      rocksPath + "luarocks/cmd/write_rockspec.lua",
		"luarocks.manif.writer":            rocksPath + "luarocks/manif/writer.lua",
		"luarocks.remove":                  rocksPath + "luarocks/remove.lua",
		"luarocks.pack":                    rocksPath + "luarocks/pack.lua",
		"luarocks.build":                   rocksPath + "luarocks/build.lua",
		"luarocks.cmd.make":                rocksPath + "luarocks/cmd/make.lua",
		"luarocks.cmd.build":               rocksPath + "luarocks/cmd/build.lua",
		"luarocks.cmd.install":             rocksPath + "luarocks/cmd/install.lua",
		"luarocks.cmd.list":                rocksPath + "luarocks/cmd/list.lua",
		"luarocks.download":                rocksPath + "luarocks/download.lua",
		"luarocks.cmd.download":            rocksPath + "luarocks/cmd/download.lua",
		"luarocks.cmd.search":              rocksPath + "luarocks/cmd/search.lua",
		"luarocks.cmd.pack":                rocksPath + "luarocks/cmd/pack.lua",
		"luarocks.cmd.new_version":         rocksPath + "luarocks/cmd/new_version.lua",
		"luarocks.cmd.purge":               rocksPath + "luarocks/cmd/purge.lua",
		"luarocks.cmd.init":                rocksPath + "luarocks/cmd/init.lua",
		"luarocks.cmd.lint":                rocksPath + "luarocks/cmd/lint.lua",
		"luarocks.test":                    rocksPath + "luarocks/test.lua",
		"luarocks.cmd.test":                rocksPath + "luarocks/cmd/test.lua",
		"luarocks.cmd.which":               rocksPath + "luarocks/cmd/which.lua",
		"luarocks.cmd.remove":              rocksPath + "luarocks/cmd/remove.lua",
		"luarocks.upload.multipart":        rocksPath + "luarocks/upload/multipart.lua",
		"luarocks.upload.api":              rocksPath + "luarocks/upload/api.lua",
		"luarocks.cmd.upload":              rocksPath + "luarocks/cmd/upload.lua",
		"luarocks.cmd.doc":                 rocksPath + "luarocks/cmd/doc.lua",
		"luarocks.cmd.unpack":              rocksPath + "luarocks/cmd/unpack.lua",
		"luarocks.cmd.config":              rocksPath + "luarocks/cmd/config.lua",
		"luarocks.require":                 rocksPath + "luarocks/require.lua",
		"luarocks.build.cmake":             rocksPath + "luarocks/build/cmake.lua",
		"luarocks.build.make":              rocksPath + "luarocks/build/make.lua",
		"luarocks.build.command":           rocksPath + "luarocks/build/command.lua",
		"luarocks.fetch.cvs":               rocksPath + "luarocks/fetch/cvs.lua",
		"luarocks.fetch.svn":               rocksPath + "luarocks/fetch/svn.lua",
		"luarocks.fetch.sscm":              rocksPath + "luarocks/fetch/sscm.lua",
		"luarocks.fetch.git":               rocksPath + "luarocks/fetch/git.lua",
		"luarocks.fetch.git_file":          rocksPath + "luarocks/fetch/git_file.lua",
		"luarocks.fetch.git_http":          rocksPath + "luarocks/fetch/git_http.lua",
		"luarocks.fetch.git_https":         rocksPath + "luarocks/fetch/git_https.lua",
		"luarocks.fetch.git_ssh":           rocksPath + "luarocks/fetch/git_ssh.lua",
		"luarocks.fetch.hg":                rocksPath + "luarocks/fetch/hg.lua",
		"luarocks.fetch.hg_http":           rocksPath + "luarocks/fetch/hg_http.lua",
		"luarocks.fetch.hg_https":          rocksPath + "luarocks/fetch/hg_https.lua",
		"luarocks.fetch.hg_ssh":            rocksPath + "luarocks/fetch/hg_ssh.lua",
		"luarocks.admin.cache":             rocksPath + "luarocks/admin/cache.lua",
		"luarocks.admin.cmd.refresh_cache": rocksPath + "luarocks/admin/cmd/refresh_cache.lua",
		"luarocks.admin.index":             rocksPath + "luarocks/admin/index.lua",
		"luarocks.admin.cmd.add":           rocksPath + "luarocks/admin/cmd/add.lua",
		"luarocks.admin.cmd.remove":        rocksPath + "luarocks/admin/cmd/remove.lua",
		"luarocks.admin.cmd.make_manifest": rocksPath + "luarocks/admin/cmd/make_manifest.lua",
	}
}()

// boot creates the gopher-lua VM, installs the custom os.getenv and the
// glr_getwd global, preloads every embedded module, and caches the LState on
// e.lstate for the engine's lifetime. It is invoked exactly once via
// e.bootOnce.Do. The LState is intentionally NOT closed here — it lives as long
// as the engine.
//
// LState reuse vs tt: tt opens a fresh lua.NewState per command and closes it,
// so each command re-initializes LuaRocks config. We reuse one cached LState to
// amortize the preload cost, which means LuaRocks' module-level state —
// notably core.cfg, guarded by cfg.initialized in the tarantool fork — persists
// across calls. This is safe here because cfg.Tree and cfg.Tarantool are fixed
// for the engine's lifetime, so the cached config stays correct;
// TestLuaEngine_ReusedLState_SequentialMakes is the regression gate. The one
// known caveat is cfg.rocks_servers, which the fork *prepends* per --server, so
// repeated calls passing different InstallOpts.Servers on the SAME engine
// accumulate sources; a caller needing isolated server sets constructs a new
// *Rocks (the same "want isolation → new client" rule).
func (e *luaEngine) boot() error {
	// Materialize the LuaRocks config file that aligns the tree layout with the
	// native backend (tt-style subdirs). The vendored cfg.lua does NOT
	// consume hardcoded.lua's ROCKS_SUBDIR / LUA_MODULES_*_SUBDIR keys (those
	// were tt patches); left to its defaults it would write to
	// <tree>/lib/luarocks/rocks-5.1 and <tree>/share/lua/5.1, diverging from the
	// native engine's <tree>/share/tarantool/rocks etc. We point LUAROCKS_CONFIG
	// at a generated file that sets the three path subdirs, which cfg.init
	// deep-merges over the defaults. This is the LuaRocks-native override
	// mechanism and keeps the no-Setenv invariant intact: LUAROCKS_CONFIG is
	// served via the in-VM os.getenv shim (envOverride), never os.Setenv.
	err := e.writeConfigFile()
	if err != nil {
		return err
	}

	L := lua.NewState() // full stdlibs; do NOT skip OpenLibs

	// Install the custom os.getenv and glr_getwd BEFORE preloading, since
	// hardcoded.lua reads them at require time.
	osTbl, ok := L.GetField(L.Get(lua.EnvironIndex), "os").(*lua.LTable)
	if !ok {
		return errors.New("luaEngine: os table missing from Lua environment")
	}

	L.SetField(osTbl, "getenv", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		if v, ok := e.envOverride[key]; ok {
			L.Push(lua.LString(v))

			return 1
		}

		if v, ok := os.LookupEnv(key); ok {
			L.Push(lua.LString(v))

			return 1
		}

		L.Push(lua.LNil)

		return 1
	}))

	L.SetGlobal("glr_getwd", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(e.cfg.WorkingDir))

		return 1
	}))

	// Normalize io.open mode strings. LuaRocks' luarocks/fs/lua.lua opens files
	// with PUC-Lua-5.1 mode strings of the form "<mode>+b" (e.g. "r+b", "w+b",
	// "a+b") for fs.is_writable / fs.copy_binary. gopher-lua's io.open accepts
	// only the "<mode>b+" ordering ("rb+", "wb+", "ab+") and raises "invalid
	// option" otherwise, crashing the command (exit 99). Wrap the original
	// io.open to rewrite the "+b" tail to "b+" before delegating; every other
	// mode passes through untouched. This is a VM-compatibility shim, not a
	// behavior change — the resulting file is opened in the identical mode.
	ioTbl, ok := L.GetField(L.Get(lua.EnvironIndex), "io").(*lua.LTable)
	if !ok {
		return errors.New("luaEngine: io table missing from Lua environment")
	}

	origOpen, ok := L.GetField(ioTbl, "open").(*lua.LFunction)
	if !ok {
		return errors.New("luaEngine: io.open missing from Lua environment")
	}

	L.SetField(ioTbl, "open", L.NewFunction(func(L *lua.LState) int {
		// Collect every argument so we can re-dispatch verbatim, rewriting
		// only the mode (arg 2) when it uses the unsupported "+b" ordering.
		top := L.GetTop()

		args := make([]lua.LValue, top)

		for i := 1; i <= top; i++ {
			args[i-1] = L.Get(i)
		}

		if top >= ioOpenMinArgsForMode {
			if ls, ok := args[1].(lua.LString); ok {
				args[1] = lua.LString(normalizeOpenMode(string(ls)))
			}
		}

		L.Push(origOpen)

		for _, a := range args {
			L.Push(a)
		}
		// io.open returns (file) on success or (nil, errmsg, errno) on failure;
		// propagate all of them so LuaRocks' error handling is unchanged.
		L.Call(len(args), lua.MultRet)

		return L.GetTop() - top
	}))

	// Intercept os.exit. The vendored LuaRocks CLI terminates every command
	// path with os.exit (luarocks/cmd.lua), which gopher-lua maps to Go's
	// os.Exit — fatal for our in-process VM (one long-lived LState for the
	// engine lifetime). Replace it with a function that raises a Lua error
	// carrying the requested exit code (prefixed with osExitSentinel) so the
	// surrounding DoString unwinds back into call() instead of killing the
	// process. call() maps code 0 to a nil error and any non-zero code to a
	// descriptive Go error.
	L.SetField(osTbl, "exit", L.NewFunction(func(L *lua.LState) int {
		code := L.OptInt(1, 0)
		L.RaiseError("%s%d", osExitSentinel, code)

		return 0
	}))

	// Route the base print() through the VM's io.stdout. gopher-lua's stock
	// print writes via fmt.Print to the host process stdout, bypassing the
	// io.stdout field that redirectIO swaps for an in-memory buffer. A
	// handful of LuaRocks commands emit user-facing data with print rather than
	// util.printout — notably `config <key>` (cmd/config.lua print_entry prints
	// string values via print). Without this shim that output would escape to
	// the real stdout and the captured string callViaState returns would be
	// empty, breaking the Config data parse. The shim tab-joins its args and
	// appends a newline exactly like PUC-Lua print, then writes to whatever
	// io.stdout currently is — so it honors the redirect and the no-Setenv
	// invariant (in-VM handle only, never the host stdout or os.Setenv).
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		ioTbl, ok := L.GetField(L.Get(lua.EnvironIndex), "io").(*lua.LTable)
		if !ok {
			return 0
		}

		out := L.GetField(ioTbl, "stdout")
		writeFn := L.GetField(out, "write")
		top := L.GetTop()

		parts := make([]string, top)

		for i := 1; i <= top; i++ {
			parts[i-1] = lua.LVAsString(L.Get(i))
		}

		line := strings.Join(parts, "\t") + "\n"

		L.Push(writeFn)
		L.Push(out)
		L.Push(lua.LString(line))
		L.Call(printWriteArgCount, 0)

		return 0
	}))

	preload := L.GetField(L.GetField(L.Get(lua.EnvironIndex), "package"), "preload")

	for modName, path := range luaPreloadMap {
		src, err := luarocksembed.FS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("luaEngine: read embedded %s: %w", path, err)
		}

		mod, err := L.LoadString(string(src))
		if err != nil {
			return fmt.Errorf("luaEngine: load %s: %w", modName, err)
		}

		L.SetField(preload, modName, mod)
	}

	e.lstate = L

	return nil
}

// luaConfigContents builds the LuaRocks config file the engine generates. It
// serves two purposes:
//
//  1. Tree-layout parity with the native backend (tree/paths.go):
//     rocks_subdir=/share/tarantool/rocks (RocksDir),
//     lua_modules_path=/share/tarantool (DeployLuaDir),
//     lib_modules_path=/lib/tarantool (DeployLibDir). These mirror
//     hardcoded.lua's ROCKS_SUBDIR / LUA_MODULES_*_SUBDIR, which this vendored
//     cfg.lua does not read. cfg.init deep-merges them over defaults.
//
//  2. Working-directory anchoring without host mutation. The shell-out fs
//     backend (luarocks.fs.tools) resolves the base directory for every
//     `cd <dir> && <cmd>` it runs from `cfg.variables.PWD` (default "pwd"),
//     which would return the host process cwd — wrong for an in-process engine
//     whose logical cwd is cfg.WorkingDir. We override PWD to echo WorkingDir,
//     so relative build paths (e.g. builtin `cp src/foo.lua`) resolve against
//     WorkingDir. This replaces a host os.Chdir (forbidden) with a
//     per-command cd into the engine's configured working directory.
//
// workDir is single-quote-escaped for the Lua string literal and shell echo.
func luaConfigContents(workDir string) string {
	escaped := strings.ReplaceAll(workDir, `'`, `'\''`)

	return fmt.Sprintf(`-- Generated by go-luarocks luaEngine. Aligns tree layout with the native
-- backend (tree/paths.go) and anchors the shell-out cwd to WorkingDir. Do not
-- edit by hand.
rocks_subdir = "/share/tarantool/rocks"
lua_modules_path = "/share/tarantool"
lib_modules_path = "/lib/tarantool"
variables = {
   PWD = "echo '%s'",
}
`, escaped)
}

// writeConfigFile materializes the generated config to a temp path and records
// LUAROCKS_CONFIG in the env-override map so cfg.init loads it. Content depends
// on cfg.WorkingDir, so it is regenerated per engine.
func (e *luaEngine) writeConfigFile() error {
	dir, err := os.MkdirTemp("", "go-luarocks-cfg-")
	if err != nil {
		return fmt.Errorf("luaEngine: create config dir: %w", err)
	}

	e.configDir = dir // tracked for finalizer cleanup

	path := filepath.Join(dir, "config-5.1.lua")

	if err := os.WriteFile(path, []byte(luaConfigContents(e.cfg.WorkingDir)), luaConfigFileMode); err != nil {
		return fmt.Errorf("luaEngine: write config file: %w", err)
	}

	e.envOverride["LUAROCKS_CONFIG"] = path

	return nil
}

// dispatch routes argv to the active dispatch backend. callImpl is a test seam
// (nil in production); when nil, the real embedded-VM path callViaState runs.
// It returns the stdout the LuaRocks command printed plus any error.
func (e *luaEngine) dispatch(argv []string) (string, error) {
	if e.callImpl != nil {
		return e.callImpl(argv)
	}

	return e.callViaState(argv)
}

// call is a thin error-only wrapper over callViaState, retained for the boot
// smoke tests in lua_test.go that predate the unified seam. New code uses
// dispatch/callViaState directly.
func (e *luaEngine) call(argv []string) error {
	_, err := e.callViaState(argv)

	return err
}

// callViaState dispatches argv through extra/wrapper.lua's exec(). It serializes
// access to the single-threaded LState via e.mu, lazily booting on first
// use. progname is fixed to the default value. Each arg is single-quote-
// escaped for embedding in the Lua single-quoted dispatch string.
//
// stdout/stderr capture: before running the command, callViaState
// replaces the Lua VM's io.stdout and io.stderr fields with buffer-backed file
// tables (newWriterFile) pointing at in-memory Go buffers, restoring the
// originals afterward. LuaRocks emits all user-facing text through
// util.printout/printerr, which write to io.stdout/io.stderr, so this captures
// it. After the command finishes, the captured text is drained into e.logger at
// info level (slog.Default() if e.logger is nil); the no-Setenv invariant holds
// — we touch only the in-VM io handles, never the host process stdout/stderr or
// os.Setenv. The
// captured stdout string is returned so data-returning methods (e.g. Pack) can
// parse it.
func (e *luaEngine) callViaState(argv []string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.bootOnce.Do(func() {
		e.bootErr = e.boot()
	})

	if e.bootErr != nil {
		return "", e.bootErr
	}

	var stdout, stderr bytes.Buffer

	restore := e.redirectIO(&stdout, &stderr)
	defer restore()

	var dispatch string
	if len(argv) == 0 {
		dispatch = "t=require('extra.wrapper').exec('go-luarocks')"
	} else {
		quoted := make([]string, len(argv))
		for i, arg := range argv {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `\'`) + "'"
		}

		dispatch = fmt.Sprintf("t=require('extra.wrapper').exec('go-luarocks', %s)",
			strings.Join(quoted, ", "))
	}

	doErr := e.lstate.DoString(dispatch)

	// Drain whatever the command printed into the logger before interpreting
	// the result, so even a failing command's diagnostics reach the caller.
	restore()
	e.drainOutput(argv, stdout.String(), stderr.String())
	out := stdout.String()

	if doErr == nil {
		// The wrapper returned normally (no os.exit). Treat as success.
		return out, nil
	}
	// The CLI almost always unwinds via our os.exit replacement, surfacing as
	// a Lua error whose message embeds osExitSentinel + the exit code. A code
	// of 0 is success; any non-zero code (or a non-sentinel error) is a real
	// failure surfaced verbatim.
	if code, ok := parseOsExit(doErr.Error()); ok {
		if code == 0 {
			return out, nil
		}

		return out, fmt.Errorf("luaEngine: %v exited with code %d", argv, code)
	}

	return out, doErr
}

// redirectIO points the VM's io.stdout and io.stderr at the supplied Go
// buffers by installing buffer-backed file userdata via io.output()/io.errput.
// It returns a function that restores the original handles; the function is
// idempotent so callViaState can both defer it and call it eagerly. Only the
// in-VM io handles are touched.
func (e *luaEngine) redirectIO(stdout, stderr io.Writer) func() {
	L := e.lstate

	ioTbl, ok := L.GetField(L.Get(lua.EnvironIndex), "io").(*lua.LTable)
	if !ok {
		return func() {}
	}

	origOut := L.GetField(ioTbl, "stdout")
	origErr := L.GetField(ioTbl, "stderr")

	L.SetField(ioTbl, "stdout", newWriterFile(L, stdout))
	L.SetField(ioTbl, "stderr", newWriterFile(L, stderr))

	restored := false

	return func() {
		if restored {
			return
		}

		restored = true

		L.SetField(ioTbl, "stdout", origOut)
		L.SetField(ioTbl, "stderr", origErr)
	}
}

// newWriterFile builds a Lua table that quacks like an io file handle for the
// subset of methods LuaRocks' util.printout/printerr use: write (called as
// f:write(...)) and the no-op flush/close. All bytes go to w.
func newWriterFile(L *lua.LState, w io.Writer) *lua.LTable { //nolint:gocritic // L is the conventional gopher-lua LState receiver name used throughout
	tbl := L.NewTable()
	write := L.NewFunction(func(L *lua.LState) int {
		// arg 1 is the file table (self); the rest are the strings to write.
		top := L.GetTop()
		for i := 2; i <= top; i++ {
			v := L.Get(i)
			if v == lua.LNil {
				continue
			}

			if _, err := io.WriteString(w, lua.LVAsString(v)); err != nil {
				L.RaiseError("go-luarocks io capture: %v", err)
			}
		}

		L.Push(tbl) // io files return self for chaining

		return 1
	})
	noop := L.NewFunction(func(L *lua.LState) int {
		L.Push(tbl)

		return 1
	})

	L.SetField(tbl, "write", write)
	L.SetField(tbl, "flush", noop)
	L.SetField(tbl, "close", noop)

	return tbl
}

// drainOutput logs captured stdout/stderr at info level so command diagnostics
// are not lost. e.logger is used when set; otherwise slog.Default().
func (e *luaEngine) drainOutput(argv []string, stdout, stderr string) {
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}

	cmd := strings.Join(argv, " ")
	if s := strings.TrimRight(stdout, "\n"); s != "" {
		logger.Info("luaEngine stdout", "cmd", cmd, "output", s)
	}

	if s := strings.TrimRight(stderr, "\n"); s != "" {
		logger.Info("luaEngine stderr", "cmd", cmd, "output", s)
	}
}

// packedPathRE matches the line upstream luarocks pack prints on success:
// `Packed: <path>` (luarocks/pack.lua report_and_sign_local_file).
var packedPathRE = regexp.MustCompile(`(?m)^Packed:\s*(.+?)\s*$`)

// parsePackPath extracts the produced rock path from pack's captured stdout.
// It returns ("", false) when no `Packed:` line is present so the caller can
// raise a real error rather than fabricating a path (no silent fallback).
func parsePackPath(stdout string) (string, bool) {
	m := packedPathRE.FindStringSubmatch(stdout)
	if m == nil {
		return "", false
	}

	return m[1], true
}

// wrotePathRE matches the line LuaRocks prints after writing a rockspec file:
//   - new_version: `Wrote <path>` (cmd/new_version.lua).
//   - write_rockspec: `Wrote template at <path> -- you should now edit ...`
//     (cmd/write_rockspec.lua).
//
// Both share the `Wrote ` / `Wrote template at ` prefix; the alternation
// captures the path and stops the write_rockspec variant before its trailing
// " -- you should now edit and finish it." hint.
var wrotePathRE = regexp.MustCompile(`(?m)^Wrote (?:template at )?(.+?)(?: -- .*)?\s*$`)

// parseWrotePath extracts the written rockspec path from new_version /
// write_rockspec stdout. Returns ("", false) when no `Wrote` line is present so
// the caller raises a real error rather than fabricating a path.
func parseWrotePath(stdout string) (string, bool) {
	m := wrotePathRE.FindStringSubmatch(stdout)
	if m == nil {
		return "", false
	}

	return m[1], true
}

// parseSearchResults parses the --porcelain listing search.print_result_tree
// emits: one tab-separated record per match,
// "<name>\t<version>\t<arch>\t<repo>\t<namespace>". Title lines are suppressed
// under --porcelain, so every non-blank line with at least three tab fields is
// a result; lines that do not match that shape are skipped (defensive against
// stray diagnostics that may share the buffer). Name, Version and Server (the
// repo URL, field index 3) are surfaced.
func parseSearchResults(stdout string) []SearchResult {
	var out []SearchResult

	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < searchFieldsMin {
			continue
		}

		out = append(out, SearchResult{
			Name:    fields[0],
			Version: fields[1],
			Server:  fields[3],
		})
	}

	return out
}

// normalizeOpenMode rewrites a PUC-Lua-5.1 file mode string into the form
// gopher-lua's io.open accepts. gopher-lua rejects the "<mode>+b" ordering
// (r+b, w+b, a+b) but accepts the equivalent "<mode>b+" ordering (rb+, wb+,
// ab+); both denote the same update-binary mode. Any other mode (including
// already-normalized or non-binary modes) is returned unchanged.
func normalizeOpenMode(mode string) string {
	switch mode {
	case "r+b":
		return "rb+"
	case "w+b":
		return "wb+"
	case "a+b":
		return "ab+"
	default:
		return mode
	}
}

// parseOsExit extracts the exit code from a DoString error message produced by
// the engine's os.exit replacement. The gopher-lua error string embeds the
// sentinel somewhere after a position prefix; returns (code, true) on a match.
func parseOsExit(msg string) (int, bool) {
	_, after, ok := strings.Cut(msg, osExitSentinel)
	if !ok {
		return 0, false
	}

	rest := after
	// The code runs up to the first non-digit (gopher-lua may append a
	// stack traceback after the message).
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}

	if end == 0 {
		return 0, false
	}

	code := 0
	for i := range end {
		code = code*decimalBase + int(rest[i]-'0')
	}

	return code, true
}

// --- Engine interface implementation ---
//
// The five methods the native backend also serves (Install, Build, Make, Pack,
// Unpack) build the upstream argv and dispatch; the remaining methods map to
// upstream-only commands.

// globalArgs returns the LuaRocks global options that must precede the
// subcommand name. --tree is ALWAYS emitted so the lua backend installs into
// the SAME tree the native engine uses (backend parity); it reads the engine's
// cfg.Tree. servers, when non-empty, append a --server <s> per entry (the
// global option lives on the main parser, before the subcommand).
func (e *luaEngine) globalArgs(servers []string) []string {
	argv := make([]string, 0, globalArgsTreePair+argsPerServer*len(servers))
	argv = append(argv, "--tree", e.cfg.Tree)

	for _, s := range servers {
		argv = append(argv, "--server", s)
	}

	return argv
}

// depsModeArg maps a DepsPolicy to upstream's --deps-mode choices
// {all, one, order, none}. DepsAll→"all", DepsNone→"none". DepsOnlyNew has no
// exact upstream equivalent; "order" (use the current tree plus those below it
// on rocks_trees) is the closest match to the "only new" documented intent.
func depsModeArg(p DepsPolicy) string {
	switch p {
	case DepsAll:
		return "all"
	case DepsNone:
		return "none"
	case DepsOnlyNew:
		// Closest upstream match to "only new" — see DepsPolicy doc.
		return "order"
	default:
		return "all"
	}
}

func (e *luaEngine) Install(ctx context.Context, name string, opts InstallOpts) error {
	argv := e.globalArgs(opts.Servers)

	argv = append(argv, "install", name)

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	argv = append(argv, "--deps-mode", depsModeArg(opts.Deps))
	_, err := e.dispatch(argv)

	return err
}

func (e *luaEngine) Build(ctx context.Context, specPath string, opts BuildOpts) error {
	argv := e.globalArgs(nil)

	argv = append(argv, "build", specPath)

	if opts.Keep {
		argv = append(argv, "--keep")
	}

	_, err := e.dispatch(argv)

	return err
}

func (e *luaEngine) Make(ctx context.Context, opts MakeOpts) error {
	argv := e.globalArgs(nil)
	argv = append(argv, "make")
	// Upstream make searches the current dir (glr_getwd) when no rockspec
	// positional is given; only append it when explicitly set.
	if opts.RockspecPath != "" {
		argv = append(argv, opts.RockspecPath)
	}

	_, err := e.dispatch(argv)

	return err
}

func (e *luaEngine) Pack(ctx context.Context, target string, opts PackOpts) (string, error) {
	// opts.SrcOnly: upstream pack has no dedicated flag — it produces a
	// .src.rock when target is a rockspec and a binary .rock when target is an
	// installed rock name (luarocks/cmd/pack.lua). There is no clean argv
	// expression to force src-only for an installed-rock target through this
	// path, so SrcOnly is honored implicitly by the target kind. We do NOT
	// silently swallow it: when SrcOnly is set against a non-rockspec target it
	// simply has no effect here, left for a follow-up rather than guessed.
	argv := e.globalArgs(nil)
	argv = append(argv, "pack", target)

	out, err := e.dispatch(argv)
	if err != nil {
		return "", err
	}

	path, ok := parsePackPath(out)
	if !ok {
		return "", fmt.Errorf("luaEngine: pack %s succeeded but no 'Packed:' path line found in output: %q", target, out)
	}

	return path, nil
}

func (e *luaEngine) Unpack(ctx context.Context, archive, destDir string) error {
	// Upstream unpack has no destination flag: it creates a directory derived
	// from the rock name inside the current working dir (luarocks/cmd/unpack.lua
	// run_unpacker → fs.make_dir(dir_name)). The VM's cwd is reported by
	// glr_getwd, which returns cfg.WorkingDir. destDir is therefore not
	// expressible as an argv flag here; the caller controls the extraction root
	// via cfg.WorkingDir. We do not invent a flag or silently drop destDir —
	// the parameter's effect is documented as bounded by cfg.WorkingDir.
	_ = destDir
	argv := e.globalArgs(nil)
	argv = append(argv, "unpack", archive)
	_, err := e.dispatch(argv)

	return err
}

// Remove runs `luarocks remove`. It is tree-scoped: the --tree global precedes
// the subcommand. name is the rock; opts.Version narrows to one version.
func (e *luaEngine) Remove(ctx context.Context, name string, opts RemoveOpts) error {
	argv := e.globalArgs(nil)

	argv = append(argv, "remove", name)

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	if opts.Force {
		argv = append(argv, "--force")
	}

	if opts.ForceFast {
		argv = append(argv, "--force-fast")
	}

	argv = append(argv, "--deps-mode", depsModeArg(opts.Deps))
	_, err := e.dispatch(argv)

	return err
}

// Purge runs `luarocks purge` against the engine's tree (the mandatory --tree
// global is always emitted).
func (e *luaEngine) Purge(ctx context.Context, opts PurgeOpts) error {
	argv := e.globalArgs(nil)

	argv = append(argv, "purge")

	if opts.OldVersions {
		argv = append(argv, "--old-versions")
	}

	if opts.Force {
		argv = append(argv, "--force")
	}

	if opts.ForceFast {
		argv = append(argv, "--force-fast")
	}

	_, err := e.dispatch(argv)

	return err
}

// Search runs `luarocks search --porcelain` and parses the tab-separated
// listing into []SearchResult. --porcelain is always passed so the output is
// machine-parseable. Errors loud if the command fails.
func (e *luaEngine) Search(ctx context.Context, pattern string, opts SearchOpts) ([]SearchResult, error) {
	argv := e.globalArgs(opts.Servers)

	argv = append(argv, "search", pattern)

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	if opts.Source {
		argv = append(argv, "--source")
	}

	if opts.Binary {
		argv = append(argv, "--binary")
	}

	if opts.All {
		argv = append(argv, "--all")
	}

	argv = append(argv, "--porcelain")

	out, err := e.dispatch(argv)
	if err != nil {
		return nil, err
	}
	// An empty result set is a legitimate outcome (a successful search that
	// matched nothing prints no listing lines under --porcelain); return an
	// empty slice, not an error.
	return parseSearchResults(out), nil
}

// Download runs `luarocks download` and returns the path of the downloaded
// file. NOTE: the embedded LuaRocks 3.9.2 does not print the saved path —
// cmd/download.lua emits nothing on success and copies the file into the
// current directory (= cfg.WorkingDir via glr_getwd). Rather than fabricate a
// path (no silent fallback), Download diffs the download directory across
// the dispatch and returns the file that appeared; on ambiguity (overwrite or
// >1 new file) it returns the containing directory. Success returns a nil error.
func (e *luaEngine) Download(ctx context.Context, name string, opts DownloadOpts) (string, error) {
	argv := e.globalArgs(opts.Servers)

	argv = append(argv, "download", name)

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	if opts.All {
		argv = append(argv, "--all")
	}

	if opts.Source {
		argv = append(argv, "--source")
	}

	if opts.Rockspec {
		argv = append(argv, "--rockspec")
	}

	if opts.Arch != "" {
		argv = append(argv, "--arch", opts.Arch)
	}
	// The embedded LuaRocks 3.9.2 (download.lua) prints no saved path, so we can't
	// parse one. Instead, snapshot the download directory (fs.current_dir() ==
	// cfg.WorkingDir) before and after and report the file that appeared. This
	// is a real path, never fabricated; on success the error is always nil.
	before := dirFiles(e.cfg.WorkingDir)

	if _, err := e.dispatch(argv); err != nil {
		return "", err
	}

	var added []string

	for name := range dirFiles(e.cfg.WorkingDir) {
		if !before[name] {
			added = append(added, name)
		}
	}

	if len(added) == 1 {
		return filepath.Join(e.cfg.WorkingDir, added[0]), nil
	}
	// Zero new files (an existing file was overwritten on re-download) or more
	// than one (ambiguous): the exact filename can't be uniquely identified.
	// Return the directory the rock was downloaded into — honest, not fabricated.
	return e.cfg.WorkingDir, nil
}

// dirFiles returns the set of regular-file names directly in dir. A missing or
// empty dir yields an empty set (no error) — used by Download to diff the
// download directory across a dispatch.
func dirFiles(dir string) map[string]bool {
	out := map[string]bool{}
	if dir == "" {
		return out
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}

	for _, ent := range entries {
		if ent.Type().IsRegular() {
			out[ent.Name()] = true
		}
	}

	return out
}

// Lint runs `luarocks lint <specPath>`. lint takes only the rockspec
// positional. Non-zero exit (syntax error) surfaces as a real error.
func (e *luaEngine) Lint(ctx context.Context, specPath string, opts LintOpts) error {
	_ = opts // lint has no tunable flags
	argv := []string{"lint", specPath}
	_, err := e.dispatch(argv)

	return err
}

// NewVersion runs `luarocks new_version <specPath>` and returns the path of the
// written rockspec parsed from the `Wrote <path>` line. Fails loud if that line
// is absent.
func (e *luaEngine) NewVersion(ctx context.Context, specPath string, opts NewVersionOpts) (string, error) {
	argv := []string{"new_version", specPath}
	if opts.NewVersion != "" {
		argv = append(argv, opts.NewVersion)
	}

	if opts.NewURL != "" {
		argv = append(argv, opts.NewURL)
	}

	if opts.Dir != "" {
		argv = append(argv, "--dir", opts.Dir)
	}

	if opts.Tag != "" {
		argv = append(argv, "--tag", opts.Tag)
	}

	out, err := e.dispatch(argv)
	if err != nil {
		return "", err
	}

	path, ok := parseWrotePath(out)
	if !ok {
		return "", fmt.Errorf("luaEngine: new_version %s succeeded but no 'Wrote' path line found in output: %q", specPath, out)
	}

	return path, nil
}

// WriteRockspec runs `luarocks write_rockspec` and returns the path of the
// written rockspec parsed from the `Wrote template at <path>` line. Fails loud
// if that line is absent. The wrapper command name uses an underscore.
func (e *luaEngine) WriteRockspec(ctx context.Context, url string, opts WriteRockspecOpts) (string, error) {
	argv := []string{"write_rockspec"}
	// Positionals are name, version, location in that order; upstream infers
	// missing leading ones. Emit name/version only when set, then the url
	// (location) last.
	if opts.Name != "" {
		argv = append(argv, opts.Name)
	}

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	if url != "" {
		argv = append(argv, url)
	}

	if opts.Output != "" {
		argv = append(argv, "--output", opts.Output)
	}

	if opts.License != "" {
		argv = append(argv, "--license", opts.License)
	}

	if opts.Summary != "" {
		argv = append(argv, "--summary", opts.Summary)
	}

	if opts.Detailed != "" {
		argv = append(argv, "--detailed", opts.Detailed)
	}

	if opts.Homepage != "" {
		argv = append(argv, "--homepage", opts.Homepage)
	}

	if opts.LuaVersions != "" {
		argv = append(argv, "--lua-versions", opts.LuaVersions)
	}

	if opts.RockspecFormat != "" {
		argv = append(argv, "--rockspec-format", opts.RockspecFormat)
	}

	if opts.Tag != "" {
		argv = append(argv, "--tag", opts.Tag)
	}

	if opts.Lib != "" {
		argv = append(argv, "--lib", opts.Lib)
	}

	out, err := e.dispatch(argv)
	if err != nil {
		return "", err
	}

	path, ok := parseWrotePath(out)
	if !ok {
		return "", fmt.Errorf("luaEngine: write_rockspec %s succeeded but no 'Wrote' path line found in output: %q", url, out)
	}

	return path, nil
}

// Doc runs `luarocks doc`. It is tree-scoped (it resolves an installed rock),
// so the --tree global precedes the subcommand.
func (e *luaEngine) Doc(ctx context.Context, name string, opts DocOpts) error {
	argv := e.globalArgs(nil)

	argv = append(argv, "doc", name)

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	if opts.Home {
		argv = append(argv, "--home")
	}

	if opts.List {
		argv = append(argv, "--list")
	}

	_, err := e.dispatch(argv)

	return err
}

// Test runs `luarocks test <specPath>`. It is tree-scoped (test installs deps
// into the tree), so the --tree global precedes the subcommand. opts.Args are
// passed through as the trailing suite arguments.
func (e *luaEngine) Test(ctx context.Context, specPath string, opts TestOpts) error {
	argv := e.globalArgs(nil)

	argv = append(argv, "test", specPath)

	if opts.Prepare {
		argv = append(argv, "--prepare")
	}

	if opts.TestType != "" {
		argv = append(argv, "--test-type", opts.TestType)
	}

	argv = append(argv, opts.Args...)
	_, err := e.dispatch(argv)

	return err
}

// Config runs `luarocks config` and returns the captured stdout (the printed
// value for a key, or the whole config when no key is given). When the command
// writes (Value or Unset set) there is no value to print and the trimmed empty
// output is returned. Fails loud on command error.
func (e *luaEngine) Config(ctx context.Context, opts ConfigOpts) (string, error) {
	argv := []string{"config"}
	if opts.Key != "" {
		argv = append(argv, opts.Key)
	}

	if opts.Value != "" {
		argv = append(argv, opts.Value)
	}

	if opts.Unset {
		argv = append(argv, "--unset")
	}

	if opts.Scope != "" {
		argv = append(argv, "--scope", opts.Scope)
	}

	if opts.JSON {
		argv = append(argv, "--json")
	}

	out, err := e.dispatch(argv)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(out, "\n"), nil
}

// Upload runs `luarocks upload <specPath>`. Void: success is an exit-0 dispatch.
func (e *luaEngine) Upload(ctx context.Context, specPath string, opts UploadOpts) error {
	argv := []string{"upload", specPath}
	if opts.SrcRock != "" {
		argv = append(argv, opts.SrcRock)
	}

	if opts.SkipPack {
		argv = append(argv, "--skip-pack")
	}

	if opts.APIKey != "" {
		argv = append(argv, "--api-key", opts.APIKey)
	}

	if opts.TempKey != "" {
		argv = append(argv, "--temp-key", opts.TempKey)
	}

	if opts.Force {
		argv = append(argv, "--force")
	}

	if opts.Sign {
		argv = append(argv, "--sign")
	}

	_, err := e.dispatch(argv)

	return err
}

// InitProject runs `luarocks init` in cfg.WorkingDir. Void.
func (e *luaEngine) InitProject(ctx context.Context, opts InitProjectOpts) error {
	argv := []string{"init"}
	if opts.Name != "" {
		argv = append(argv, opts.Name)
	}

	if opts.Version != "" {
		argv = append(argv, opts.Version)
	}

	if opts.Reset {
		argv = append(argv, "--reset")
	}

	_, err := e.dispatch(argv)

	return err
}

// Admin runs `luarocks admin <subCmd> <args...>`. The wrapper.lua exec()
// dispatches a leading "admin" arg to the admin command table, so argv begins
// with "admin" followed by the subcommand and its verbatim args. Admin
// commands are server-scoped, not tree-scoped, so no --tree global is emitted;
// opts.Server appends --server when set. Void.
func (e *luaEngine) Admin(ctx context.Context, subCmd string, args []string, opts AdminOpts) error {
	argv := []string{"admin", subCmd}

	argv = append(argv, args...)

	if opts.Server != "" {
		argv = append(argv, "--server", opts.Server)
	}

	if opts.Force {
		argv = append(argv, "--force")
	}

	_, err := e.dispatch(argv)

	return err
}
