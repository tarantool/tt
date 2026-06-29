package client

import (
	"context"
)

// Engine is the backend contract for every write operation a Rocks facade
// can perform. The public *Rocks write methods are pure delegations to the
// active engine; the engine is selected once at New() time and is
// final for the lifetime of the returned *Rocks.
//
// Engine contains every operation both backends (native, lua) must
// answer for. Where a backend cannot perform an operation, its method
// returns rocks.ErrNotImplemented — never a silent no-op or zero-value
// success. Callers discriminate with errors.Is(err, rocks.ErrNotImplemented).
//
// Read operations (List, Show, Which, ReadTreeManifest) are intentionally
// NOT part of Engine: they are served by the native r.store regardless of
// the selected backend, so they stay on *Rocks directly.
//
//nolint:interfacebloat // Engine deliberately mirrors the full LuaRocks command surface; splitting it would fragment the single backend contract.
type Engine interface {
	// The five operations the native backend already implements.
	Install(ctx context.Context, name string, opts InstallOpts) error
	Build(ctx context.Context, specPath string, opts BuildOpts) error
	Make(ctx context.Context, opts MakeOpts) error
	Pack(ctx context.Context, target string, opts PackOpts) (string, error)
	Unpack(ctx context.Context, archive, destDir string) error

	// The thirteen operations not yet implemented by the native backend.
	// Until a backend implements them they return rocks.ErrNotImplemented.
	Remove(ctx context.Context, name string, opts RemoveOpts) error
	Purge(ctx context.Context, opts PurgeOpts) error
	Search(ctx context.Context, pattern string, opts SearchOpts) ([]SearchResult, error)
	Download(ctx context.Context, name string, opts DownloadOpts) (string, error)
	Lint(ctx context.Context, specPath string, opts LintOpts) error
	NewVersion(ctx context.Context, specPath string, opts NewVersionOpts) (string, error)
	WriteRockspec(ctx context.Context, url string, opts WriteRockspecOpts) (string, error)
	Doc(ctx context.Context, name string, opts DocOpts) error
	Test(ctx context.Context, specPath string, opts TestOpts) error
	Config(ctx context.Context, opts ConfigOpts) (string, error)
	Upload(ctx context.Context, specPath string, opts UploadOpts) error
	InitProject(ctx context.Context, opts InitProjectOpts) error
	Admin(ctx context.Context, subCmd string, args []string, opts AdminOpts) error
}

// Backend selects which Engine implementation a Rocks facade uses. The zero
// value is BackendNative — the pure-Go backend that has always served this
// package — so New(cfg) with no options keeps existing behavior.
type Backend int

const (
	// BackendNative is the pure-Go implementation (nativeEngine). It is the
	// default (zero value) and serves all five currently-implemented write
	// operations.
	BackendNative Backend = iota
	// BackendLua selects the gopher-lua backend, which boots an embedded
	// LuaRocks VM lazily on the first write call and serves every upstream
	// LuaRocks command.
	BackendLua
)

// String renders the Backend for debug logging.
func (b Backend) String() string {
	switch b {
	case BackendNative:
		return "native"
	case BackendLua:
		return "lua"
	default:
		return "unknown"
	}
}

// Option configures a Rocks at construction time. Apply via New(cfg, opts...).
type Option func(*Rocks)

// WithBackend selects the Engine backend for the constructed Rocks. The
// selection is applied at New() time and is final for the returned *Rocks.
func WithBackend(b Backend) Option {
	return func(r *Rocks) {
		r.backend = b
	}
}
