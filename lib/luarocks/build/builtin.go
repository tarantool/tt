package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

const (
	// dirPerm is the mode used when creating destination directories.
	dirPerm os.FileMode = 0o750
	// exePerm is the mode applied to installed executable artifacts.
	exePerm os.FileMode = 0o750
	// dirOwnerRWX is OR-ed into a copied directory's mode so the owner can
	// always traverse/write the recreated tree.
	dirOwnerRWX os.FileMode = 0o700
	// initialArgsCap pre-sizes the cc argument slice to avoid early regrows.
	initialArgsCap = 16
)

// runBuiltin implements the `builtin` build backend.
//
// It iterates spec.Build.Modules and dispatches per module shape:
//
//   - Path != "" and ends in ".lua"  → copy file (no compile)
//   - Path != "" and ends in ".c"    → single-source C compile
//   - Sources non-empty (table form) → multi-source C compile with the
//     per-module incdirs/libdirs/libraries/defines applied
//
// After modules, spec.Build.Install.{Lua,Lib,Bin,Conf} entries are copied
// into the matching subdirectory under destDir (lua/lib/bin/conf). The
// downstream tree.Deploy step is responsible for moving these to their
// final deploy paths; this backend only writes them under destDir.
//
// spec.Build.CopyDirectories entries are copied recursively from srcDir
// to destDir/<dir> verbatim.
//
// If ANY module needs a C compile and cfg.Tarantool.IncludeDir is empty,
// the function returns ErrMissingTarantoolHeaders before invoking cc.
//
// destDir is the rock's staging tree root (the "build" subdir the facade
// hands us). Files live at:
//
//	destDir/lua/<a>/<b>/<c>.lua            for ["a.b.c"] = ".../c.lua"
//	destDir/lib/<a>/<b>/<c>.so             for compiled C modules
//	destDir/lua|lib|bin|conf/<entries...>  for build.install.*
//	destDir/<dir>/...                      for build.copy_directories
func runBuiltin(ctx context.Context, spec *rocks.Rockspec, srcDir, destDir string, cfg rocks.Config) error {
	flags := DeriveFlags(cfg)

	// Stable iteration so error reports are deterministic.
	names := make([]string, 0, len(spec.Build.Modules))
	for n := range spec.Build.Modules {
		names = append(names, n)
	}

	sort.Strings(names)

	// Pre-flight: any C compile needs headers.
	for _, n := range names {
		m := spec.Build.Modules[n]
		if needsCC(m) && cfg.Tarantool.IncludeDir == "" {
			return fmt.Errorf("build: module %q: %w", n, rocks.ErrMissingTarantoolHeaders)
		}
	}

	for _, n := range names {
		m := spec.Build.Modules[n]

		switch {
		case m.Path != "" && strings.HasSuffix(m.Path, ".lua"):
			err := installLuaModule(srcDir, destDir, n, m.Path)
			if err != nil {
				return fmt.Errorf("build: module %q: %w", n, err)
			}
		case m.Path != "" && strings.HasSuffix(m.Path, ".c"):
			err := compileModule(ctx, srcDir, destDir, n,
				rocks.Module{Sources: []string{m.Path}}, flags, cfg)
			if err != nil {
				return fmt.Errorf("build: module %q: %w", n, err)
			}
		case len(m.Sources) > 0:
			err := compileModule(ctx, srcDir, destDir, n, m, flags, cfg)
			if err != nil {
				return fmt.Errorf("build: module %q: %w", n, err)
			}
		default:
			return fmt.Errorf("build: module %q: empty entry (no path, no sources)", n)
		}
	}

	err := installBuildInstall(srcDir, destDir, spec.Build.Install)
	if err != nil {
		return fmt.Errorf("build: install: %w", err)
	}

	for _, d := range spec.Build.CopyDirectories {
		err := copyDir(filepath.Join(srcDir, d), filepath.Join(destDir, d))
		if err != nil {
			return fmt.Errorf("build: copy_directories %q: %w", d, err)
		}
	}

	return nil
}

// needsCC reports whether the module entry compiles a C source.
func needsCC(m rocks.Module) bool {
	if m.Path != "" && strings.HasSuffix(m.Path, ".c") {
		return true
	}

	return len(m.Sources) > 0
}

// moduleSlashPath converts a dotted module name to the slashed
// destination subpath. "foo.bar.baz" → "foo/bar/baz".
func moduleSlashPath(name string) string {
	return strings.ReplaceAll(name, ".", "/")
}

// installLuaModule copies a single .lua module file from srcDir/path into
// destDir/lua/<dotted-to-slashed-name>.lua.
func installLuaModule(srcDir, destDir, name, path string) error {
	dst := filepath.Join(destDir, "lua", moduleSlashPath(name)+".lua")

	return copyFile(filepath.Join(srcDir, path), dst)
}

// compileModule builds a single .so via one cc invocation:
//
//	$CC $CFLAGS [-Iincdir...] [-Ddefine...] $LIBFLAG \
//	    -o destDir/lib/<slashed>.so <sources...> \
//	    [-Llibdir...] [-llibname...] $LDFLAGS
//
// All sources are passed in a single cc call (do not reimplement
// build infrastructure; rely on cc to handle multiple .c inputs).
func compileModule(ctx context.Context, srcDir, destDir, name string, m rocks.Module, flags Flags, cfg rocks.Config) error {
	out := filepath.Join(destDir, "lib", moduleSlashPath(name)+flags.Ext)

	err := os.MkdirAll(filepath.Dir(out), dirPerm)
	if err != nil {
		return err
	}

	args := make([]string, 0, initialArgsCap)

	args = append(args, flags.CFLAGS...)

	for _, d := range m.Defines {
		args = append(args, "-D"+d)
	}

	for _, inc := range m.Incdirs {
		args = append(args, "-I"+inc)
	}

	args = append(args, flags.LIBFLAG...)

	args = append(args, "-o", out)

	for _, s := range m.Sources {
		args = append(args, filepath.Join(srcDir, s))
	}

	for _, l := range m.Libdirs {
		args = append(args, "-L"+l)
	}

	for _, lib := range m.Libraries {
		args = append(args, "-l"+lib)
	}

	args = append(args, flags.LDFLAGS...)

	return runCmd(ctx, flags.CC, args, srcDir, buildEnv(cfg))
}

// installBuildInstall processes the four sub-maps of build.install. Each
// map is destination-name → source-path-relative-to-srcDir. The
// destination layout under destDir is:
//
//	install.lua  → destDir/lua/<dotted-to-slashed-key>.lua
//	install.lib  → destDir/lib/<dotted-to-slashed-key>.<ext>  (mode 0o750)
//	install.bin  → destDir/bin/<key-as-given>                 (mode 0o750)
//	install.conf → destDir/conf/<key-as-given>
//
// tree.Deploy may later re-copy these into the deploy tree; that
// duplication is acceptable for now and the install dirs win since deploy
// runs after build.
func installBuildInstall(srcDir, destDir string, bi rocks.BuildInstall) error {
	type sub struct {
		name      string
		entries   map[string]string
		subdir    string
		isModule  bool
		isExe     bool
		extension string // forced extension, e.g. ".so"; empty = key as-given
	}

	subs := []sub{
		{name: "install.lua", entries: bi.Lua, subdir: "lua", isModule: true, extension: ".lua"},
		{name: "install.lib", entries: bi.Lib, subdir: "lib", isModule: true, isExe: true, extension: ".so"},
		{name: "install.bin", entries: bi.Bin, subdir: "bin", isExe: true},
		{name: "install.conf", entries: bi.Conf, subdir: "conf"},
	}
	for _, s := range subs {
		keys := make([]string, 0, len(s.entries))
		for k := range s.entries {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			src := filepath.Join(srcDir, s.entries[k])

			var rel string
			if s.isModule {
				rel = moduleSlashPath(k) + s.extension
			} else {
				rel = k
			}

			dst := filepath.Join(destDir, s.subdir, rel)

			err := copyFile(src, dst)
			if err != nil {
				return fmt.Errorf("%s[%q]: %w", s.name, k, err)
			}

			if s.isExe {
				err := os.Chmod(dst, exePerm) //nolint:gosec // installed binaries/libraries must be executable
				if err != nil {
					return fmt.Errorf("%s[%q]: chmod: %w", s.name, k, err)
				}
			}
		}
	}

	return nil
}

// copyFile copies a file preserving the source file's regular-file mode
// bits (lower 9 bits). Parent directories of dst are created with dirPerm
// (0o750).
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	in, err := os.Open(src) //nolint:gosec // src is a rockspec-derived build path, internally controlled
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	st, err := in.Stat()
	if err != nil {
		return err
	}

	mode := st.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // dst is a rockspec-derived build path, internally controlled
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()

		return err
	}

	return out.Close()
}

// copyDir recursively copies src into dst. Symlinks are not specially
// handled — they are dereferenced (matches upstream luarocks behavior for
// copy_directories on unix).
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|dirOwnerRWX)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Resolve to real file and copy its contents.
			return copyFile(p, target)
		}

		return copyFile(p, target)
	})
}
