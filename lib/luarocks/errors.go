package rocks

import "errors"

// Sentinel errors callers can branch on with errors.Is.
//
// These replace the "deep cc/cmake failure or generic os.exit from
// the Lua side" pattern that tt currently string-matches on. Every facade
// method documents which of these it can return.
var (
	// ErrMissingTarantoolHeaders signals that a build backend needs the
	// Tarantool development headers but Config.Tarantool.IncludeDir is
	// unset or does not contain `tarantool/module.h`.
	ErrMissingTarantoolHeaders = errors.New("rocks: tarantool headers not found")

	// ErrMissingTarantoolBinary signals that an operation requires the
	// `tarantool` binary on disk (e.g. shelling out for capability probes)
	// and Config.Tarantool.Executable is unset or does not point at an
	// executable.
	ErrMissingTarantoolBinary = errors.New("rocks: tarantool binary not found")

	// ErrUnsupportedCommand signals that the v1 Exec shim received a
	// luarocks subcommand outside the v1 in-scope set (download, search,
	// remove, purge, lint, new_version, write_rockspec, doc, test, config,
	// upload, admin/*).
	ErrUnsupportedCommand = errors.New("rocks: unsupported command")

	// ErrUnsupportedRockspecFeature signals that a rockspec used a feature
	// the evaluator does not implement (e.g. build.type outside the
	// {"builtin","cmake","make","command","none"} set, or a platforms
	// block we don't recognize). Fail loud rather than silently skip.
	ErrUnsupportedRockspecFeature = errors.New("rocks: unsupported rockspec feature")

	// ErrNotImplemented signals that the active backend (Engine) has no
	// implementation for the invoked method. It is the SOLE error a method
	// returns in that case — no silent no-op, no zero-value success.
	// Callers branch on it with errors.Is(err, ErrNotImplemented) to
	// detect an unsupported operation for the selected backend.
	ErrNotImplemented = errors.New("rocks: method not implemented by this backend")
)
