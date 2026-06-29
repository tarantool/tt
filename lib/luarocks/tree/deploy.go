package tree

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// Module kinds, matching the LuaRocks install layout: pure-Lua modules go
// under the "lua" tree, native libraries under the "lib" tree.
const (
	kindLua = "lua"
	kindLib = "lib"
)

// Deploy copies the artifacts of a built rock into the tree, producing
// the per-rock RockManifest (path → md5 hex) that the caller then writes
// to <install-dir>/rock_manifest via t.Store.WriteRock.
//
// Two source directories are consulted:
//   - srcDir holds the original rockspec source tree (the output of
//     fetch.Fetch). Deploy reads .lua modules, prebuilt .so/.dylib
//     modules, install.{lua,lib,bin,conf} files, and copy_directories
//     from srcDir at the rockspec-relative paths.
//   - buildDir holds compiled artifacts produced by the build subsystem.
//     Deploy reads .c-source-derived .so files and table-form module
//     artifacts from buildDir at the canonical slashed path
//     `<dotted/slashed>.so`.
//
// For callers without a separate build phase (pure-.lua rocks, or unit
// tests where srcDir already contains every artifact), pass srcDir as
// both arguments — the lookup is layered and both forms work.
//
// What gets copied where:
//
//   - build.modules: dotted module name → slashed path; .lua → DeployLuaDir,
//     .so → DeployLibDir.
//   - build.install.lua / .lib: the KEY is a dotted module name (upstream
//     is_module_path=true) → module_to_path(key) subdir; a .lua source is
//     renamed to <last-segment>.lua. See installDest.
//   - build.install.conf: the KEY is a literal path joined under ConfDir.
//   - build.install.bin: the KEY is a literal path. When BinWrap is set the
//     real script lands in the per-rock <install-dir>/bin and BinDir gets an
//     executable launcher (0o755); when nil the script is copied verbatim
//     (0o755).
//   - build.copy_directories: each entry is a directory under srcDir copied
//     recursively into the per-rock install dir (only a "doc" dir is recorded
//     in the rock_manifest; see copyTree).
//
// Collisions with previously-deployed versions are handled via munged
// filenames; see conflicts.go.
func (t *Tree) Deploy(spec *rocks.Rockspec, srcDir, buildDir string) (*rocks.RockManifest, error) {
	if spec == nil {
		return nil, errors.New("tree.Deploy: nil spec")
	}

	if spec.Package == "" || spec.Version == "" {
		return nil, errors.New("tree.Deploy: spec missing Package/Version")
	}

	if buildDir == "" {
		buildDir = srcDir
	}

	rm := &rocks.RockManifest{
		Lua:  map[string]string{},
		Lib:  map[string]string{},
		Bin:  map[string]string{},
		Conf: map[string]string{},
		Doc:  map[string]string{},
	}

	// 1) build.modules
	for modName, mod := range spec.Build.Modules {
		srcPath, dstPath, kind, err := resolveModule(srcDir, buildDir, modName, mod, t.Paths)
		if err != nil {
			return nil, err
		}

		dstPath, err = resolveCollision(dstPath, spec.Package, spec.Version)
		if err != nil {
			return nil, err
		}

		sum, err := copyFile(srcPath, dstPath, filePerm)
		if err != nil {
			return nil, fmt.Errorf("tree.Deploy: module %q: %w", modName, err)
		}

		rel, err := filepath.Rel(deployDirForKind(t.Paths, kind), dstPath)
		if err != nil {
			return nil, err
		}

		rel = filepath.ToSlash(rel)

		switch kind {
		case kindLua:
			rm.Lua[rel] = sum
		case kindLib:
			rm.Lib[rel] = sum
		}
	}

	// 2) build.install.*
	//
	// Upstream's install_files (luarocks/build.lua) interprets the string KEYS
	// of each section differently depending on the section (prepare_install_dirs
	// sets is_module_path per section):
	//
	//   - lua, lib  → is_module_path=true:  the key is a DOTTED MODULE NAME. The
	//     destination subdir is module_to_path(key) (dots→slashes, minus the last
	//     segment); a .lua source is renamed to <last-segment>.lua, anything else
	//     keeps the source basename.
	//   - bin, conf → is_module_path=false: the key is a literal slash path; the
	//     destination is dir_name(key)/base_name(key), which filepath.Join
	//     reproduces directly.
	type instGroup struct {
		entries      map[string]string
		dir          string
		mode         os.FileMode
		dst          map[string]string
		isModulePath bool
	}

	groups := []instGroup{
		{spec.Build.Install.Lua, t.DeployLuaDir(), 0o644, rm.Lua, true},
		{spec.Build.Install.Lib, t.DeployLibDir(), 0o644, rm.Lib, true},
		{spec.Build.Install.Conf, t.ConfDir(), 0o644, rm.Conf, false},
	}
	for _, g := range groups {
		for key, srcRel := range g.entries {
			src := filepath.Join(srcDir, srcRel)
			dst := installDest(g.dir, key, srcRel, g.isModulePath)

			dst, err := resolveCollision(dst, spec.Package, spec.Version)
			if err != nil {
				return nil, err
			}

			sum, err := copyFile(src, dst, g.mode)
			if err != nil {
				return nil, fmt.Errorf("tree.Deploy: install %q: %w", key, err)
			}

			rel, err := filepath.Rel(g.dir, dst)
			if err != nil {
				return nil, err
			}

			g.dst[filepath.ToSlash(rel)] = sum
		}
	}

	// 2b) build.install.bin — is_module_path=false, like conf, but upstream
	// additionally deploys the public <tree>/bin entry as a launcher
	// (repos.deploy_files with should_wrap_bin_scripts → fs.wrap_script). When
	// t.BinWrap is set we reproduce that two-step layout: the real script lands
	// in the per-rock <install-dir>/bin and <tree>/bin gets the wrapper. When
	// unset we copy verbatim (legacy behavior).
	installDir := t.InstallDir(spec.Package, spec.Version)

	for key, srcRel := range spec.Build.Install.Bin {
		src := filepath.Join(srcDir, srcRel)
		rel := filepath.Clean(key) // is_module_path=false: dir_name/base_name == the key path

		if t.BinWrap == nil {
			dst, err := resolveCollision(filepath.Join(t.BinDir(), rel), spec.Package, spec.Version)
			if err != nil {
				return nil, err
			}

			sum, err := copyFile(src, dst, execPerm)
			if err != nil {
				return nil, fmt.Errorf("tree.Deploy: install bin %q: %w", key, err)
			}

			r, err := filepath.Rel(t.BinDir(), dst)
			if err != nil {
				return nil, err
			}

			rm.Bin[filepath.ToSlash(r)] = sum

			continue
		}

		// Wrapped: real script → per-rock <install-dir>/bin/<rel>.
		realScript := filepath.Join(installDir, "bin", rel)

		sum, err := copyFile(src, realScript, execPerm)
		if err != nil {
			return nil, fmt.Errorf("tree.Deploy: install bin %q: %w", key, err)
		}

		rm.Bin[filepath.ToSlash(filepath.Join("bin", rel))] = sum

		// Public launcher → <tree>/bin/<rel>, execing the interpreter on the
		// real script (matches fs/unix.lua wrap_script byte-for-byte).
		wrapperDst, err := resolveCollision(filepath.Join(t.BinDir(), rel), spec.Package, spec.Version)
		if err != nil {
			return nil, err
		}

		if err := os.MkdirAll(filepath.Dir(wrapperDst), dirPerm); err != nil {
			return nil, fmt.Errorf("tree.Deploy: mkdir bin %q: %w", key, err)
		}

		body := t.binWrapperScript(realScript, spec.Package, spec.Version)
		if err := os.WriteFile(wrapperDst, []byte(body), execPerm); err != nil {
			return nil, fmt.Errorf("tree.Deploy: write bin wrapper %q: %w", key, err)
		}
	}

	// 3) build.copy_directories — recursive copy into the per-rock install dir.

	for _, dir := range spec.Build.CopyDirectories {
		src := filepath.Join(srcDir, dir)

		dst := filepath.Join(installDir, dir)

		err := copyTree(src, dst, rm, dir)
		if err != nil {
			return nil, fmt.Errorf("tree.Deploy: copy_directories[%q]: %w", dir, err)
		}
	}

	return rm, nil
}

// resolveModule maps a single build.modules entry to (srcPath, dstPath, kind).
// kind is "lua" or "lib"; the caller writes into rm.Lua / rm.Lib accordingly.
//
// Source-directory layering:
//   - srcDir for .lua module paths and prebuilt .so/.dylib paths from the
//     rockspec (these live in the original source tree).
//   - buildDir for compiled artifacts the build subsystem produced
//     (C-source modules and table-form modules emit `<slashName>.so` into
//     buildDir).
//
// Dotted module names become slashed paths; the last segment becomes the
// filename. So "foo.bar.baz" with a .lua source becomes "foo/bar/baz.lua"
// under DeployLuaDir, and a .so module of the same name becomes
// "foo/bar/baz.so" under DeployLibDir.
func resolveModule(srcDir, buildDir, modName string, mod rocks.Module, p Paths) (src, dst, kind string, err error) {
	slashName := strings.ReplaceAll(modName, ".", string(filepath.Separator))

	switch {
	case mod.Path != "":
		ext := strings.ToLower(filepath.Ext(mod.Path))
		switch ext {
		case ".lua":
			src = filepath.Join(srcDir, mod.Path)
			dst = filepath.Join(p.DeployLuaDir(), slashName+".lua")
			kind = kindLua
		case ".so", ".dylib":
			// Tarantool/upstream luarocks deploy compiled modules with the
			// canonical platform suffix `.so`. Sources with `.dylib` on
			// macOS are renamed at deploy time to `.so` to match upstream's
			// install_files behavior. Prebuilt artifacts live in srcDir.
			src = filepath.Join(srcDir, mod.Path)
			dst = filepath.Join(p.DeployLibDir(), slashName+".so")
			kind = kindLib
		case ".c", ".cpp", ".cxx", ".cc":
			// A C source listed as Path implies the build step compiled it
			// to buildDir/lib/<slashName>.so. The "lib/" prefix matches
			// build/builtin's compileModule output layout.
			src = filepath.Join(buildDir, kindLib, slashName+".so")
			dst = filepath.Join(p.DeployLibDir(), slashName+".so")
			kind = kindLib
		default:
			err = fmt.Errorf("module %q: unsupported source extension %q", modName, ext)

			return src, dst, kind, err
		}
	case len(mod.Sources) > 0:
		// Table-form: the build subsystem produced the .so under
		// buildDir/lib/<slashName>.so (matching compileModule output).
		src = filepath.Join(buildDir, kindLib, slashName+".so")
		dst = filepath.Join(p.DeployLibDir(), slashName+".so")
		kind = kindLib
	default:
		err = fmt.Errorf("module %q: neither Path nor Sources set", modName)
	}

	return src, dst, kind, err
}

// installDest computes the on-disk destination for one build.install.<section>
// entry, mirroring upstream's install_to. For module-path sections (lua, lib)
// the key is a dotted module name: the subdir is module_to_path(key) and a .lua
// source is renamed to <last-segment>.lua. For literal sections (bin, conf) the
// key is a slash path joined directly under dir.
func installDest(dir, key, srcRel string, isModulePath bool) string {
	if !isModulePath {
		return filepath.Join(dir, key)
	}

	sub := moduleToPathDir(key)

	filename := filepath.Base(srcRel)
	if strings.HasSuffix(strings.ToLower(filename), ".lua") {
		filename = moduleLastSegment(key) + ".lua"
	}

	return filepath.Join(dir, filepath.FromSlash(sub), filename)
}

// moduleToPathDir mirrors upstream luarocks path.module_to_path: it drops the
// last dot-delimited segment of a module name and converts the remaining dots
// to forward slashes, yielding the destination SUBDIRECTORY (slash-delimited,
// possibly empty). E.g. "withinstall.helper" → "withinstall/", "a.b.c" →
// "a/b/", "foo" → "".
func moduleToPathDir(mod string) string {
	if i := strings.LastIndex(mod, "."); i >= 0 {
		mod = mod[:i+1] // keep through the final dot, drop the last segment
	} else {
		mod = "" // no dot: the whole string is the last segment, removed
	}

	return strings.ReplaceAll(mod, ".", "/")
}

// moduleLastSegment returns the final dot-delimited segment of a module name
// (upstream's `modname:match("([^.]+)$")`). E.g. "withinstall.helper" →
// "helper", "foo" → "foo".
func moduleLastSegment(mod string) string {
	if i := strings.LastIndex(mod, "."); i >= 0 {
		return mod[i+1:]
	}

	return mod
}

// binWrapperScript reproduces fs/unix.lua wrap_script byte-for-byte for the
// single-tree deps case: a /bin/sh launcher that sets package.path/cpath to the
// tree's deploy dirs, attempts to load luarocks.loader (guarded — a native tree
// has none, the pcall absorbs it), registers the rock context, then execs the
// interpreter on the real script. Path entries mirror path.package_paths:
// "<lua_dir>/?.lua;<lua_dir>/?/init.lua" and "<lib_dir>/?.so".
func (t *Tree) binWrapperScript(realScript, name, version string) string {
	lua := filepath.ToSlash(t.DeployLuaDir())
	lib := filepath.ToSlash(t.DeployLibDir())
	lpath := lua + "/?.lua;" + lua + "/?/init.lua"
	lcpath := lib + "/?.so"

	luainit := "package.path=" + luaQuote(lpath+";") + "..package.path" +
		";package.cpath=" + luaQuote(lcpath+";") + "..package.cpath" +
		";local k,l,_=pcall(require," + luaQuote("luarocks.loader") + ") _=k and l.add_context(" +
		luaQuote(name) + "," + luaQuote(version) + ")"

	return "#!/bin/sh\n\nLUAROCKS_SYSCONFDIR=" + shellQuote(t.BinWrap.Sysconfdir) +
		" exec " + shellQuote(t.BinWrap.Interpreter) +
		" -e " + shellQuote(luainit) +
		" " + shellQuote(realScript) + ` "$@"` + "\n"
}

// luaQuote mirrors util.LQ — Lua's string.format("%q", s). For the wrapper's
// path/identifier inputs (no embedded quotes, backslashes, or newlines) this is
// a plain double-quoted string; the escapes below keep it faithful regardless.
func luaQuote(s string) string {
	var b strings.Builder

	b.WriteByte('"')

	for i := range len(s) {
		switch c := s[i]; c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteByte(c)
		}
	}

	b.WriteByte('"')

	return b.String()
}

// shellQuote mirrors fs/unix.lua unix.Q: wrap in single quotes, escaping any
// embedded single quote as '\”.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func deployDirForKind(p Paths, kind string) string {
	switch kind {
	case kindLua:
		return p.DeployLuaDir()
	case kindLib:
		return p.DeployLibDir()
	}

	return p.Tree
}

// copyFile writes src → dst with mode, mkdir-p the parent, and returns
// the md5 hex of the bytes written.
func copyFile(src, dst string, mode os.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", filepath.Dir(dst), err)
	}

	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", src, err)
	}

	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return "", fmt.Errorf("create %q: %w", dst, err)
	}

	h := md5.New()

	w := io.MultiWriter(out, h)
	if _, err := io.Copy(w, in); err != nil {
		_ = out.Close()

		return "", fmt.Errorf("copy %q->%q: %w", src, dst, err)
	}

	if err := out.Close(); err != nil {
		return "", fmt.Errorf("close %q: %w", dst, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyTree recursively copies src → dst, computing an md5 for every file.
// Only files under a "doc" top dir are recorded — into rm.Doc, keyed by the
// rock-root-relative path so the manifest key matches the on-disk layout.
// Files from any other copy_directories entry are written to disk but NOT
// recorded in the rock_manifest: they don't belong to the lua/lib/bin/conf
// buckets, and keeping the manifest closed over those known buckets is
// simpler than inventing a category for arbitrary copied content.
func copyTree(src, dst string, rm *rocks.RockManifest, topName string) error {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %q: %w", src, err)
	}

	if !st.IsDir() {
		return fmt.Errorf("%q is not a directory", src)
	}

	return filepath.Walk(src, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, dirPerm)
		}

		sum, err := copyFile(p, target, info.Mode().Perm())
		if err != nil {
			return err
		}

		if topName == "doc" {
			key := filepath.ToSlash(rel)
			rm.Doc[key] = sum
		}

		return nil
	})
}
