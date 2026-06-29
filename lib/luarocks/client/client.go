// Package client implements the Rocks facade — the keystone public API that
// composes the manif, rockspec, fetch, build, tree, deps and remote subsystems
// into LuaRocks operations. The native backend implements eight of them —
// Install, Build, Make, List, Show, Which, Pack, Unpack — and returns
// rocks.ErrNotImplemented for the rest; the lua backend covers the full
// upstream command set. The complete method set is the Engine interface
// (engine.go).
//
// Why this lives in a sub-package rather than at the module root:
//
//   - deps/ imports rocks (root) for shared data types (Rockspec, Version,
//     VersionConstraint, …).
//   - The facade needs to invoke deps.Resolve.
//   - A direct rocks → deps import would create a cycle.
//
// The root rocks package retains the data types and interfaces; the
// operational Rocks struct + methods live here in the client package.
// Callers spell it `client.New(cfg)`.
//
// Subsystem references:
//
//   - rockspec.Eval / MergePlatforms / RuntimePlatforms / Validate
//   - fetch.Fetch / FetchWith
//   - build.RunBackend
//   - tree.Open / tree.Tree.Deploy / tree.Tree.Which
//   - manif.FileStore (default ManifestStore)
//   - deps.Resolve
//   - remote.HTTPRemoteIndex (default RemoteIndex)
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
	"slices"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
	"github.com/tarantool/tt/lib/luarocks/manif"
	"github.com/tarantool/tt/lib/luarocks/remote"
	"github.com/tarantool/tt/lib/luarocks/rockspec"
	"github.com/tarantool/tt/lib/luarocks/tree"
)

// Rocks is the public facade. Construct via New(Config). All operations
// take a context and read configuration from r.cfg — no hidden global
// state, no os.Getwd / os.Chdir, no os.Setenv.
//
// Write operations delegate to r.engine, which is selected once at New()
// time per r.backend and is final for the lifetime of the facade.
// Read operations (List, Show, Which, ReadTreeManifest) are served directly
// from r.store regardless of backend.
type Rocks struct {
	cfg     rocks.Config
	store   rocks.ManifestStore
	index   rocks.RemoteIndex
	logger  *slog.Logger
	backend Backend
	engine  Engine
}

// InstallOpts tunes Install.
type InstallOpts struct {
	// Version, if set, narrows the candidate set to those matching this
	// constraint (parsed via deps.ParseConstraints). Empty means "any
	// version satisfying the rockspec's transitive constraints" — i.e.
	// the resolver picks the newest.
	Version string

	// Servers overrides r.cfg.Servers for this Install. Empty means use
	// the facade's configured servers.
	Servers []string

	// Deps controls whether transitive dependencies are also installed.
	Deps DepsPolicy
}

// DepsPolicy mirrors upstream's `--deps-mode` flag.
type DepsPolicy int

const (
	// DepsAll resolves and installs every transitive dependency.
	DepsAll DepsPolicy = 0
	// DepsNone installs only the named rock; missing deps cause Install
	// to fail.
	DepsNone DepsPolicy = 1
	// DepsOnlyNew installs deps that aren't already in the tree.
	DepsOnlyNew DepsPolicy = 2
)

// BuildOpts tunes Build.
type BuildOpts struct {
	// Keep, if true, leaves the staging build directory in place after a
	// successful build for debugging. Default removes it.
	Keep bool
}

// MakeOpts tunes Make.
type MakeOpts struct {
	// RockspecPath, if non-empty, names the rockspec to build. Empty
	// means search r.cfg.WorkingDir for exactly one `*.rockspec`.
	RockspecPath string
}

// PackOpts tunes Pack.
type PackOpts struct {
	// SrcOnly, if true, produces a `.src.rock` containing the rockspec
	// and original source archive rather than a deployable `.rock`.
	SrcOnly bool
}

// InstalledRock is re-exported from the root package for caller
// convenience (so `client.InstalledRock` and `rocks.InstalledRock` both
// resolve to the same type).
type InstalledRock = rocks.InstalledRock

// ShowInfo is re-exported from the root package — see InstalledRock.
type ShowInfo = rocks.ShowInfo

// New constructs a Rocks facade from cfg. Validates the minimum required
// fields and wires up default implementations.
//
// The only error New itself returns is a descriptive error when cfg.Tree is
// empty. Tarantool-header validation is NOT done here: New does not stat
// cfg.Tarantool.IncludeDir; ErrMissingTarantoolHeaders surfaces later from the
// operations that actually need the headers (e.g. building a C-extension rock).
//
// opts apply at construction time. The default backend is BackendNative
// (the zero value); WithBackend overrides it. Backend selection is final
// for the returned *Rocks. The existing New(cfg) call shape is
// preserved by the variadic.
func New(cfg rocks.Config, opts ...Option) (*Rocks, error) {
	if cfg.Tree == "" {
		return nil, errors.New("rocks: Config.Tree is required")
	}

	r := &Rocks{
		cfg:   cfg,
		store: manif.FileStore{},
		index: &remote.HTTPRemoteIndex{
			Servers:         cfg.Servers,
			InsecureServers: cfg.InsecureServers,
		},
		logger: cfg.Logger,
	}
	if r.logger == nil {
		r.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	for _, opt := range opts {
		opt(r)
	}
	// Select the engine per r.backend. The native engine shares the same
	// cfg/store/index/logger so reads (r.store) and writes (r.engine) see
	// one consistent tree.
	native := &nativeEngine{
		cfg:    r.cfg,
		store:  r.store,
		index:  r.index,
		logger: r.logger,
	}

	switch r.backend {
	case BackendLua:
		// The gopher-lua backend. Boot is lazy: newLuaEngine does not
		// touch the VM here; the LState is created on first write call. The
		// native engine is still constructed above (reads use r.store
		// regardless of backend) — harmless if its write methods go
		// unused for the lua backend.
		r.engine = newLuaEngine(r.cfg, r.store, r.logger)
	case BackendNative:
		r.engine = native
	default:
		r.engine = native
	}

	return r, nil
}

// ReadTreeManifest is the method-style convenience for
// manif.FileStore.ReadTree against r.cfg.Tree. It returns the top-level
// manifest the tree currently advertises (composes the store).
func (r *Rocks) ReadTreeManifest() (*rocks.Manifest, error) {
	t, err := tree.Open(r.cfg)
	if err != nil {
		return nil, err
	}

	return r.store.ReadTree(t.RocksDir())
}

// Install installs `name` (with optional version constraint in
// opts.Version) into r.cfg.Tree, including transitive deps per
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
func (r *Rocks) Install(ctx context.Context, name string, opts InstallOpts) error {
	return r.engine.Install(ctx, name, opts)
}

// Build evaluates the rockspec at specPath, fetches its declared source,
// runs the build backend, and deploys the result into r.cfg.Tree.
//
// Unlike Install, Build does not perform dependency resolution — it
// assumes prerequisites are already present (matching upstream
// `luarocks build`).
func (r *Rocks) Build(ctx context.Context, specPath string, opts BuildOpts) error {
	return r.engine.Build(ctx, specPath, opts)
}

// Make is "build the rockspec found in cwd against the source already
// present in cwd" — the upstream `luarocks make` flow. It is the
// developer-iteration form of Build.
func (r *Rocks) Make(ctx context.Context, opts MakeOpts) error {
	return r.engine.Make(ctx, opts)
}

// List returns every rock currently installed in r.cfg.Tree.
func (r *Rocks) List(ctx context.Context) ([]InstalledRock, error) {
	_ = ctx // local read, no I/O cancellation needed

	t, err := tree.Open(r.cfg)
	if err != nil {
		return nil, err
	}

	m, err := r.store.ReadTree(t.RocksDir())
	if err != nil {
		if os.IsNotExist(unwrapPathErr(err)) {
			return []InstalledRock{}, nil
		}

		return nil, err
	}

	out := []InstalledRock{}

	for name, versions := range m.Repository {
		for ver := range versions {
			out = append(out, InstalledRock{Name: name, Version: ver})
		}
	}

	return out, nil
}

// Show returns the summary info for a single rock installed in the tree.
// Returns an error wrapping os.ErrNotExist when the rock is not present.
func (r *Rocks) Show(ctx context.Context, name string) (*ShowInfo, error) {
	_ = ctx

	t, err := tree.Open(r.cfg)
	if err != nil {
		return nil, err
	}

	m, err := r.store.ReadTree(t.RocksDir())
	if err != nil {
		return nil, err
	}

	versions, ok := m.Repository[name]
	if !ok {
		return nil, fmt.Errorf("rocks.Show: %q not installed: %w", name, os.ErrNotExist)
	}
	// Pick the lexicographically lowest installed version string, so the
	// choice is deterministic across runs regardless of map iteration order.
	var picked string
	for v := range versions {
		if picked == "" || v < picked {
			picked = v
		}
	}

	out := &ShowInfo{Package: name, Version: picked}

	// Load the per-rock manifest to enumerate modules.
	rockManifestPath := filepath.Join(t.InstallDir(name, picked), "rock_manifest")

	rm, err := r.store.ReadRock(rockManifestPath)
	if err == nil && rm != nil {
		for k := range rm.Lua {
			out.Modules = append(out.Modules, k)
		}

		for k := range rm.Lib {
			out.Modules = append(out.Modules, k)
		}
	}

	// Try to read the rockspec for richer info — non-fatal if missing.
	specPath := filepath.Join(t.InstallDir(name, picked), name+"-"+picked+".rockspec")
	if spec, err := rockspec.Eval(specPath, r.cfg.Rockspec); err == nil {
		out.Summary = spec.Description.Summary
		out.License = spec.Description.License
		out.Homepage = spec.Description.Homepage
		out.Dependencies = spec.Dependencies
	}

	return out, nil
}

// Which resolves a dotted Lua module name to its on-disk file path in
// r.cfg.Tree. Returns (path, true, nil) on hit; ("", false, nil) on miss.
func (r *Rocks) Which(ctx context.Context, module string) (string, bool, error) {
	_ = ctx

	t, err := tree.Open(r.cfg)
	if err != nil {
		return "", false, err
	}

	p, ok := t.Which(module)

	return p, ok, nil
}

// Pack produces a .rock or .src.rock archive for `target` (rock name) in
// r.cfg.WorkingDir and returns its path.
//
//   - opts.SrcOnly == false: zip the installed tree at
//     <tree>/share/tarantool/rocks/<name>/<version>/ into <name>-<ver>.rock.
//   - opts.SrcOnly == true:  zip the rockspec only into
//     <name>-<ver>.src.rock. (Source download lives outside the rockspec;
//     upstream pulls source per `source.url` and inlines it. We omit that
//     until a real packaging need surfaces — fail loud over over-engineering.)
func (r *Rocks) Pack(ctx context.Context, target string, opts PackOpts) (string, error) {
	return r.engine.Pack(ctx, target, opts)
}

// Unpack extracts `archive` (a .rock or .src.rock zip) into destDir.
func (r *Rocks) Unpack(ctx context.Context, archive, destDir string) error {
	return r.engine.Unpack(ctx, archive, destDir)
}

// --- engine-delegated operations not served by the native backend ---
//
// Each method below is a pure one-line delegation to r.engine — no
// backend-aware branching here. BackendNative returns rocks.ErrNotImplemented
// for every one of them; BackendLua runs the corresponding upstream
// LuaRocks command in the embedded VM.

// Remove uninstalls a rock from r.cfg.Tree (upstream `luarocks remove`).
// BackendNative returns rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) Remove(ctx context.Context, name string, opts RemoveOpts) error {
	return r.engine.Remove(ctx, name, opts)
}

// Purge removes all rocks from r.cfg.Tree (upstream `luarocks purge`).
// BackendNative returns rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) Purge(ctx context.Context, opts PurgeOpts) error {
	return r.engine.Purge(ctx, opts)
}

// Search queries the configured servers for rocks matching pattern (upstream
// `luarocks search`). BackendNative returns rocks.ErrNotImplemented; BackendLua
// runs upstream.
func (r *Rocks) Search(ctx context.Context, pattern string, opts SearchOpts) ([]SearchResult, error) {
	return r.engine.Search(ctx, pattern, opts)
}

// Download fetches a rock file into r.cfg.WorkingDir and returns its path
// (upstream `luarocks download`). BackendNative returns
// rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) Download(ctx context.Context, name string, opts DownloadOpts) (string, error) {
	return r.engine.Download(ctx, name, opts)
}

// Lint checks the syntax of a rockspec (upstream `luarocks lint`).
// BackendNative returns rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) Lint(ctx context.Context, specPath string, opts LintOpts) error {
	return r.engine.Lint(ctx, specPath, opts)
}

// NewVersion writes an updated rockspec for a new version and returns its path
// (upstream `luarocks new_version`). BackendNative returns
// rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) NewVersion(ctx context.Context, specPath string, opts NewVersionOpts) (string, error) {
	return r.engine.NewVersion(ctx, specPath, opts)
}

// WriteRockspec writes a starter rockspec for sources at url and returns its
// path (upstream `luarocks write_rockspec`). BackendNative returns
// rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) WriteRockspec(ctx context.Context, url string, opts WriteRockspecOpts) (string, error) {
	return r.engine.WriteRockspec(ctx, url, opts)
}

// Doc shows or lists documentation for an installed rock (upstream
// `luarocks doc`). BackendNative returns rocks.ErrNotImplemented; BackendLua
// runs upstream.
func (r *Rocks) Doc(ctx context.Context, name string, opts DocOpts) error {
	return r.engine.Doc(ctx, name, opts)
}

// Test runs a rock's test suite (upstream `luarocks test`). BackendNative
// returns rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) Test(ctx context.Context, specPath string, opts TestOpts) error {
	return r.engine.Test(ctx, specPath, opts)
}

// Config reads or writes LuaRocks configuration and returns the printed value
// (upstream `luarocks config`). BackendNative returns rocks.ErrNotImplemented;
// BackendLua runs upstream.
func (r *Rocks) Config(ctx context.Context, opts ConfigOpts) (string, error) {
	return r.engine.Config(ctx, opts)
}

// Upload publishes a rockspec to a rocks server (upstream `luarocks upload`).
// BackendNative returns rocks.ErrNotImplemented; BackendLua runs upstream.
func (r *Rocks) Upload(ctx context.Context, specPath string, opts UploadOpts) error {
	return r.engine.Upload(ctx, specPath, opts)
}

// InitProject scaffolds a LuaRocks project in r.cfg.WorkingDir (upstream
// `luarocks init`). BackendNative returns rocks.ErrNotImplemented; BackendLua
// runs upstream.
func (r *Rocks) InitProject(ctx context.Context, opts InitProjectOpts) error {
	return r.engine.InitProject(ctx, opts)
}

// Admin runs a `luarocks admin <subCmd>` repository-administration command
// (upstream `luarocks admin`). BackendNative returns rocks.ErrNotImplemented;
// BackendLua runs upstream.
func (r *Rocks) Admin(ctx context.Context, subCmd string, args []string, opts AdminOpts) error {
	return r.engine.Admin(ctx, subCmd, args, opts)
}

// --- internal helpers ---

// matchModulePath reports whether the on-disk slashed path (with extension)
// appears verbatim in the deployed file map, returning the matched key. It is
// an exact lookup; conflict-munged siblings (`<slashed>_<munge>` suffixes) are
// not matched here.
func matchModulePath(deployed map[string]string, want string) (string, bool) {
	if _, ok := deployed[want]; ok {
		return want, true
	}

	return "", false
}

// appendUnique adds s to xs if not already present.
func appendUnique(xs []string, s string) []string {
	if slices.Contains(xs, s) {
		return xs
	}

	return append(xs, s)
}

func evalAndPrepare(specPath string, cfg rocks.Config) (*rocks.Rockspec, error) {
	spec, err := rockspec.Eval(specPath, cfg.Rockspec)
	if err != nil {
		return nil, fmt.Errorf("rockspec.Eval %s: %w", specPath, err)
	}

	rockspec.MergePlatforms(spec, rockspec.RuntimePlatforms())

	if err := rockspec.Validate(spec); err != nil {
		return nil, fmt.Errorf("rockspec.Validate %s: %w", specPath, err)
	}

	return spec, nil
}

// findRockspecIn locates the rockspec of a fetched registry artifact. It
// scans ONLY the top level of dir: a bare `.rockspec` download is a single
// top-level file, and a `.src.rock` archive carries its rockspec at the
// archive root. We deliberately do NOT recurse — a .src.rock also bundles
// the rock's source tree, which may itself ship a `rockspecs/` directory of
// unrelated rockspec files (e.g. `say`), and recursing would ambiguously
// match those.
func findRockspecIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}

	var found string

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".rockspec") {
			continue
		}

		p := filepath.Join(dir, ent.Name())
		if found != "" {
			return "", fmt.Errorf("multiple .rockspec under %s: %s and %s", dir, found, p)
		}

		found = p
	}

	if found == "" {
		return "", fmt.Errorf("no .rockspec under %s", dir)
	}

	return found, nil
}

// pickNewest filters candidates by cs and returns the highest version.
// Mirrors deps.pickNewest (which is unexported); duplicating here keeps
// the client free of a deps-internal symbol dependency.
func pickNewest(candidates []rocks.VersionedRock, cs []rocks.VersionConstraint) (rocks.VersionedRock, bool) {
	var best rocks.VersionedRock

	have := false

	for _, c := range candidates {
		if !deps.Match(c.Version, cs) {
			continue
		}

		if !have || deps.Compare(c.Version, best.Version) > 0 {
			best = c
			have = true
		}
	}

	return best, have
}

func unwrapPathErr(err error) error {
	type unwrapper interface{ Unwrap() error }

	for err != nil {
		pe := &os.PathError{}
		if errors.As(err, &pe) {
			return pe.Err
		}

		u, ok := err.(unwrapper)
		if !ok {
			return err
		}

		err = u.Unwrap()
	}

	return nil
}

// zipDir recursively zips srcDir contents into outPath. Entries are
// stored with paths relative to srcDir.
func zipDir(outPath, srcDir string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}

	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)

	walkErr := filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}

		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}

		in, err := os.Open(p)
		if err != nil {
			return err
		}

		defer func() { _ = in.Close() }()

		_, err = io.Copy(w, in)

		return err
	})
	if walkErr != nil {
		_ = zw.Close()

		return walkErr
	}

	return zw.Close()
}

func zipSingleFile(outPath, srcPath, entryName string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}

	defer func() { _ = out.Close() }()

	zw := zip.NewWriter(out)

	w, err := zw.Create(entryName)
	if err != nil {
		_ = zw.Close()

		return err
	}

	if _, err := io.Copy(w, in); err != nil {
		_ = zw.Close()

		return err
	}

	return zw.Close()
}
