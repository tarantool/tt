package client

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/build"
	"github.com/tarantool/tt/lib/luarocks/deps"
	"github.com/tarantool/tt/lib/luarocks/fetch"
	"github.com/tarantool/tt/lib/luarocks/remote"
	"github.com/tarantool/tt/lib/luarocks/tree"
)

// nativeEngine is the pure-Go backend (BackendNative). It holds the same
// state the *Rocks methods used before the Engine extraction and retains
// EXACT behavioral compatibility with the pre-task client.Rocks: the
// five implemented method bodies and their private helpers are moved here
// verbatim, with only the receiver renamed from (r *Rocks) to (e *nativeEngine).
//
// The thirteen operations the native backend does not implement return
// rocks.ErrNotImplemented — never a silent no-op.
type nativeEngine struct {
	cfg    rocks.Config
	store  rocks.ManifestStore
	index  rocks.RemoteIndex
	logger *slog.Logger
}

// unpackDirMode is the permission applied to directories created while
// extracting a .rock archive: owner rwx, group rx, no world access.
const unpackDirMode = 0o750

// luaInterpreter is the interpreter name baked into generated command
// wrappers, matching the tarantool/luarocks fork's hardcoded.LUA_INTERPRETER.
const luaInterpreter = "tarantool"

// defaultSysconfDir is upstream LuaRocks' cfg.sysconfdir fallback on Unix
// (luarocks/core/cfg.lua) when neither LUAROCKS_SYSCONFDIR nor a detected
// install prefix applies.
const defaultSysconfDir = "/etc/luarocks"

// sysconfDir mirrors upstream's cfg.sysconfdir resolution for the value
// exported as LUAROCKS_SYSCONFDIR in command wrappers: honor the
// LUAROCKS_SYSCONFDIR env override, else fall back to the Unix default.
func sysconfDir() string {
	if v := os.Getenv("LUAROCKS_SYSCONFDIR"); v != "" {
		return v
	}

	return defaultSysconfDir
}

// Install installs `name` (with optional version constraint in
// opts.Version) into e.cfg.Tree, including transitive deps per
// opts.Deps. The general algorithm:
//
//  1. Query the remote index for `name` candidates.
//  2. Pick the newest version satisfying opts.Version.
//  3. Resolve transitive deps (unless DepsNone).
//  4. For each step in topo order: fetch source, eval rockspec, merge
//     platforms, validate, build, deploy, update tree manifest.
//  5. Install the requested rock itself.
//
// Returns ErrUnsupportedRockspecFeature for unrecognized build types
// (bubbled up from build.RunBackend). May also surface
// ErrMissingTarantoolHeaders when a C-extension rock is built.
func (e *nativeEngine) Install(ctx context.Context, name string, opts InstallOpts) error {
	if name == "" {
		return errors.New("rocks.Install: empty name")
	}

	idx := e.index
	if len(opts.Servers) > 0 {
		idx = &remote.HTTPRemoteIndex{
			Servers:         opts.Servers,
			InsecureServers: e.cfg.InsecureServers,
		}
	}

	cs, err := deps.ParseConstraints(opts.Version)
	if err != nil {
		return fmt.Errorf("rocks.Install: parse version %q: %w", opts.Version, err)
	}

	candidates, err := idx.Query(ctx, name)
	if err != nil {
		return fmt.Errorf("rocks.Install: query %q: %w", name, err)
	}

	root, ok := pickNewest(candidates, cs)
	if !ok {
		return fmt.Errorf("rocks.Install: no version of %q matches %q", name, opts.Version)
	}

	e.logger.Info("rocks.Install: selected", "name", name, "version", root.Version.Raw, "url", root.URL)

	// Fetch + eval root rockspec so we can resolve transitive deps.
	rootSpec, err := e.fetchAndEval(ctx, root.URL)
	if err != nil {
		return fmt.Errorf("rocks.Install: fetch/eval root: %w", err)
	}

	root.Spec = rootSpec

	var plan []rocks.InstallStep
	if opts.Deps != DepsNone {
		plan, err = deps.Resolve(ctx, rootSpec, idx)
		if err != nil {
			return fmt.Errorf("rocks.Install: resolve deps: %w", err)
		}
	}

	for _, step := range plan {
		err := e.installStep(ctx, step)
		if err != nil {
			return fmt.Errorf("rocks.Install: dep %s-%s: %w", step.Name, step.Version.Raw, err)
		}
	}

	// Finally install the requested rock itself. Its source is fetched from
	// the rockspec's source.url, exactly like every dependency step.
	rootStep := rocks.InstallStep{
		Name:     rootSpec.Package,
		Version:  root.Version,
		URL:      root.URL,
		Rockspec: rootSpec,
	}
	if err := e.installFromSource(ctx, rootStep); err != nil {
		return fmt.Errorf("rocks.Install: install root: %w", err)
	}

	return nil
}

// Build evaluates the rockspec at specPath, fetches its declared source,
// runs the build backend, and deploys the result into e.cfg.Tree.
//
// Unlike Install, Build does not perform dependency resolution — it
// assumes prerequisites are already present (matching upstream
// `luarocks build`).
func (e *nativeEngine) Build(ctx context.Context, specPath string, opts BuildOpts) error {
	spec, err := evalAndPrepare(specPath, e.cfg)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp(e.cfg.WorkingDir, "rocks-build-*")
	if err != nil {
		return fmt.Errorf("rocks.Build: mkdir tmp: %w", err)
	}

	if !opts.Keep {
		defer func() { _ = os.RemoveAll(tmp) }()
	}

	srcDir, err := e.fetchSource(ctx, spec, tmp)
	if err != nil {
		return fmt.Errorf("rocks.Build: %w", err)
	}

	return e.deployFromSource(ctx, spec, srcDir)
}

// Make is "build the rockspec found in cwd against the source already
// present in cwd" — the upstream `luarocks make` flow. It is the
// developer-iteration form of Build.
func (e *nativeEngine) Make(ctx context.Context, opts MakeOpts) error {
	specPath := opts.RockspecPath
	if specPath == "" {
		entries, err := os.ReadDir(e.cfg.WorkingDir)
		if err != nil {
			return fmt.Errorf("rocks.Make: read %s: %w", e.cfg.WorkingDir, err)
		}

		var found []string

		for _, ent := range entries {
			if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".rockspec") {
				found = append(found, filepath.Join(e.cfg.WorkingDir, ent.Name()))
			}
		}

		if len(found) == 0 {
			return fmt.Errorf("rocks.Make: no .rockspec found in %s", e.cfg.WorkingDir)
		}

		if len(found) > 1 {
			return fmt.Errorf("rocks.Make: multiple .rockspec found in %s (%v); pass MakeOpts.RockspecPath", e.cfg.WorkingDir, found)
		}

		specPath = found[0]
	}

	spec, err := evalAndPrepare(specPath, e.cfg)
	if err != nil {
		return err
	}

	return e.deployFromSource(ctx, spec, e.cfg.WorkingDir)
}

// Pack produces a .rock or .src.rock archive for `target` (rock name) in
// e.cfg.WorkingDir and returns its path.
//
//   - opts.SrcOnly == false: zip the installed tree at
//     <tree>/share/tarantool/rocks/<name>/<version>/ into <name>-<ver>.rock.
//   - opts.SrcOnly == true:  zip the rockspec only into
//     <name>-<ver>.src.rock. (Source download lives outside the rockspec;
//     upstream pulls source per `source.url` and inlines it. We omit that
//     until a real packaging need surfaces — fail loud over over-engineering.)
func (e *nativeEngine) Pack(ctx context.Context, target string, opts PackOpts) (string, error) {
	_ = ctx

	t, err := tree.Open(e.cfg)
	if err != nil {
		return "", err
	}

	m, err := e.store.ReadTree(t.RocksDir())
	if err != nil {
		return "", err
	}

	versions, ok := m.Repository[target]
	if !ok {
		return "", fmt.Errorf("rocks.Pack: %q not installed in %s", target, t.Tree)
	}

	var picked string
	for v := range versions {
		if picked == "" || v < picked {
			picked = v
		}
	}

	installDir := t.InstallDir(target, picked)

	suffix := ".rock"
	if opts.SrcOnly {
		suffix = ".src.rock"
	}

	outPath := filepath.Join(e.cfg.WorkingDir, target+"-"+picked+suffix)

	if opts.SrcOnly {
		specPath := filepath.Join(installDir, target+"-"+picked+".rockspec")

		err := zipSingleFile(outPath, specPath, target+"-"+picked+".rockspec")
		if err != nil {
			return "", fmt.Errorf("rocks.Pack: zip rockspec: %w", err)
		}
	} else {
		err := zipDir(outPath, installDir)
		if err != nil {
			return "", fmt.Errorf("rocks.Pack: zip install dir: %w", err)
		}
	}

	return outPath, nil
}

// Unpack extracts `archive` (a .rock or .src.rock zip) into destDir.
func (e *nativeEngine) Unpack(ctx context.Context, archive, destDir string) error {
	_ = ctx

	if err := os.MkdirAll(destDir, unpackDirMode); err != nil {
		return fmt.Errorf("rocks.Unpack: mkdir: %w", err)
	}

	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("rocks.Unpack: open %s: %w", archive, err)
	}

	defer func() { _ = zr.Close() }()

	cleanDest := filepath.Clean(destDir)

	for _, f := range zr.File {
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) && target != cleanDest {
			return fmt.Errorf("rocks.Unpack: entry %q escapes destDir", f.Name)
		}

		if f.FileInfo().IsDir() {
			err := os.MkdirAll(target, unpackDirMode)
			if err != nil {
				return err
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), unpackDirMode); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()

			return err
		}

		if _, err := io.Copy(out, rc); err != nil {
			_ = rc.Close()
			_ = out.Close()

			return err
		}

		_ = rc.Close()

		if err := out.Close(); err != nil {
			return err
		}
	}

	return nil
}

// --- internal helpers (used only by the five native write operations) ---

func (e *nativeEngine) installStep(ctx context.Context, step rocks.InstallStep) error {
	spec, err := e.fetchAndEval(ctx, step.URL)
	if err != nil {
		return err
	}

	step.Rockspec = spec

	return e.installFromSource(ctx, step)
}

// fetchAndEval fetches the resolver-produced rock/rockspec URL into a
// throwaway directory, locates the `.rockspec` it contains (a bare
// .rockspec, or one bundled inside a .src.rock), and evaluates it. Only the
// parsed *Rockspec is returned — the fetched directory is the registry
// artifact, NOT the rock's source tree, so it is removed before returning.
// The actual source is fetched separately from spec.Source.URL at build
// time (see installFromSource / Build), mirroring upstream luarocks which
// never builds against the rockspec download directory.
func (e *nativeEngine) fetchAndEval(ctx context.Context, urlStr string) (*rocks.Rockspec, error) {
	tmp, err := os.MkdirTemp(e.cfg.WorkingDir, "rocks-fetch-*")
	if err != nil {
		return nil, fmt.Errorf("mkdir tmp: %w", err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	srcDir, err := fetch.FetchWith(ctx, urlStr, tmp, fetch.Options{
		InsecureServers: e.cfg.InsecureServers,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", urlStr, err)
	}

	specPath, err := findRockspecIn(srcDir)
	if err != nil {
		return nil, err
	}

	return evalAndPrepare(specPath, e.cfg)
}

// installFromSource fetches the rockspec's declared source (spec.Source.URL)
// into a fresh build directory and builds+deploys against THAT — never
// against the rockspec download directory, which holds only the registry
// artifact. This mirrors nativeEngine.Build and upstream luarocks'
// fetch_sources → build flow.
func (e *nativeEngine) installFromSource(ctx context.Context, step rocks.InstallStep) error {
	spec := step.Rockspec

	tmp, err := os.MkdirTemp(e.cfg.WorkingDir, "rocks-src-*")
	if err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	srcDir, err := e.fetchSource(ctx, spec, tmp)
	if err != nil {
		return err
	}

	return e.deployFromSource(ctx, spec, srcDir)
}

// fetchSource downloads spec.Source.URL into tmp and returns the source
// root to build against. After unpacking an archive the real source usually
// lives in a single top-level subdirectory (e.g. inspect.lua-3.1.3/), and
// build.modules paths are relative to that root — so descend into it,
// mirroring upstream luarocks' fetch.find_base_dir.
func (e *nativeEngine) fetchSource(ctx context.Context, spec *rocks.Rockspec, tmp string) (string, error) {
	unpacked, err := fetch.FetchWith(ctx, spec.Source.URL, tmp, fetch.Options{
		InsecureServers: e.cfg.InsecureServers,
		Tag:             spec.Source.Tag,
		Branch:          spec.Source.Branch,
	})
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", spec.Source.URL, err)
	}

	return findSourceBaseDir(unpacked, spec)
}

// findSourceBaseDir mirrors upstream luarocks fetch.find_base_dir: pick the
// directory the rock's source actually lives in after unpacking.
//
//   - If source.dir is set and names an existing subdirectory, use it
//     (explicit override from the rockspec).
//   - Else, if dir contains exactly one entry and it is a directory, descend
//     into it (the common single-versioned-subdir tarball layout).
//   - Else, use dir as-is (flat layout: a bare module file, or a git/file
//     checkout whose files already sit at the top level).
func findSourceBaseDir(dir string, spec *rocks.Rockspec) (string, error) {
	if spec.Source.Dir != "" {
		cand := filepath.Join(dir, spec.Source.Dir)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, nil
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read source dir %s: %w", dir, err)
	}

	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(dir, entries[0].Name()), nil
	}

	return dir, nil
}

func (e *nativeEngine) deployFromSource(ctx context.Context, spec *rocks.Rockspec, srcDir string) error {
	t, err := tree.Open(e.cfg)
	if err != nil {
		return err
	}
	// Match upstream's command-wrapper deploy (repos.deploy_files →
	// fs.wrap_script): the interpreter path is LUA_BINDIR/lua_interpreter
	// (prefix/bin/tarantool), and LUAROCKS_SYSCONFDIR mirrors cfg.sysconfdir.
	t.BinWrap = &tree.BinWrap{
		Interpreter: filepath.Join(e.cfg.Tarantool.Prefix, "bin", luaInterpreter),
		Sysconfdir:  sysconfDir(),
	}

	destDir := filepath.Join(t.InstallDir(spec.Package, spec.Version), "build")
	if err := os.MkdirAll(destDir, unpackDirMode); err != nil {
		return fmt.Errorf("mkdir destDir: %w", err)
	}

	if err := build.RunBackend(ctx, spec, srcDir, destDir, e.cfg); err != nil {
		return err
	}
	// srcDir holds original .lua / install / copy_dirs files; destDir holds
	// compiled .so artifacts from the build phase. Deploy reads from both.
	rm, err := t.Deploy(spec, srcDir, destDir)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}

	rockManifestPath := filepath.Join(t.InstallDir(spec.Package, spec.Version), "rock_manifest")
	if err := e.store.WriteRock(rockManifestPath, rm); err != nil {
		return fmt.Errorf("write rock_manifest: %w", err)
	}
	// Update the top-level tree manifest.
	m, err := e.store.ReadTree(t.RocksDir())
	if err != nil {
		// Treat missing manifest as a fresh tree.
		if os.IsNotExist(unwrapPathErr(err)) {
			m = &rocks.Manifest{
				Repository:   map[string]map[string]rocks.RepoEntry{},
				Modules:      map[string][]string{},
				Commands:     map[string][]string{},
				Dependencies: map[string]map[string][]rocks.Dep{},
			}
		} else {
			return fmt.Errorf("read tree manifest: %w", err)
		}
	}

	if m.Repository == nil {
		m.Repository = map[string]map[string]rocks.RepoEntry{}
	}

	if m.Repository[spec.Package] == nil {
		m.Repository[spec.Package] = map[string]rocks.RepoEntry{}
	}

	if m.Modules == nil {
		m.Modules = map[string][]string{}
	}

	if m.Commands == nil {
		m.Commands = map[string][]string{}
	}
	// Build the per-arch entry's modules/commands index from what tree.Deploy
	// actually wrote (rm.Lua, rm.Lib, rm.Bin). Module names in the rockspec
	// are dotted; on-disk paths are slashed — invert by reading rm directly.
	entry := rocks.RepoEntry{Arch: "installed"}
	pkgVer := spec.Package + "/" + spec.Version

	if len(rm.Lua) > 0 || len(rm.Lib) > 0 {
		entry.Modules = map[string]string{}

		for modName := range spec.Build.Modules {
			// Derive on-disk path from the deployed RockManifest: prefer
			// rm.Lib (compiled .so) over rm.Lua (the .lua source).
			slashed := strings.ReplaceAll(modName, ".", "/")
			if p, ok := matchModulePath(rm.Lib, slashed+".so"); ok {
				entry.Modules[modName] = p
				m.Modules[modName] = appendUnique(m.Modules[modName], pkgVer)

				continue
			}

			if p, ok := matchModulePath(rm.Lua, slashed+".lua"); ok {
				entry.Modules[modName] = p
				m.Modules[modName] = appendUnique(m.Modules[modName], pkgVer)

				continue
			}
		}
	}

	if len(rm.Bin) > 0 {
		entry.Commands = map[string]string{}
		for binName, srcRel := range spec.Build.Install.Bin {
			entry.Commands[binName] = srcRel
			m.Commands[binName] = appendUnique(m.Commands[binName], pkgVer)
		}
	}

	m.Repository[spec.Package][spec.Version] = entry
	if err := e.store.WriteTree(t.RocksDir(), m); err != nil {
		return fmt.Errorf("write tree manifest: %w", err)
	}

	return nil
}

// --- unimplemented operations: loud ErrNotImplemented ---

func (e *nativeEngine) Remove(ctx context.Context, name string, opts RemoveOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) Purge(ctx context.Context, opts PurgeOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) Search(ctx context.Context, pattern string, opts SearchOpts) ([]SearchResult, error) {
	return nil, rocks.ErrNotImplemented
}

func (e *nativeEngine) Download(ctx context.Context, name string, opts DownloadOpts) (string, error) {
	return "", rocks.ErrNotImplemented
}

func (e *nativeEngine) Lint(ctx context.Context, specPath string, opts LintOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) NewVersion(ctx context.Context, specPath string, opts NewVersionOpts) (string, error) {
	return "", rocks.ErrNotImplemented
}

func (e *nativeEngine) WriteRockspec(ctx context.Context, url string, opts WriteRockspecOpts) (string, error) {
	return "", rocks.ErrNotImplemented
}

func (e *nativeEngine) Doc(ctx context.Context, name string, opts DocOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) Test(ctx context.Context, specPath string, opts TestOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) Config(ctx context.Context, opts ConfigOpts) (string, error) {
	return "", rocks.ErrNotImplemented
}

func (e *nativeEngine) Upload(ctx context.Context, specPath string, opts UploadOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) InitProject(ctx context.Context, opts InitProjectOpts) error {
	return rocks.ErrNotImplemented
}

func (e *nativeEngine) Admin(ctx context.Context, subCmd string, args []string, opts AdminOpts) error {
	return rocks.ErrNotImplemented
}
