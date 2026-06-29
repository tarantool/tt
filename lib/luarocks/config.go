package rocks

import "log/slog"

// Config carries every input a Rocks operation needs.
//
// WorkingDir is explicit — the facade never reads process cwd; callers pass
// WorkingDir directly.
//
// Tarantool fields come from one source of truth on the caller side; the
// library does not parse `tarantool --version` itself.
type Config struct {
	// Tree is the root of the Tarantool rocks tree to read from and install into.
	Tree string
	// WorkingDir is the base directory for relative paths and build staging;
	// the facade never reads the process cwd.
	WorkingDir string
	// Tarantool describes the Tarantool installation rocks are built against.
	Tarantool TarantoolConfig
	// Servers are the rocks-repository base URLs queried for remote operations.
	Servers []string
	// Rockspec governs the sandboxed rockspec evaluator.
	Rockspec RockspecConfig
	// Logger receives structured operation logs; nil disables logging.
	Logger *slog.Logger

	// InsecureServers lists URL hosts (no scheme) for which TLS certificate
	// verification is skipped by the http fetcher. Escape hatch: upstream
	// luarocks disables certs for `rocks.tarantool.org`; we ALLOW that opt-in
	// via this field but default to verifying.
	InsecureServers []string
}

// TarantoolConfig describes the Tarantool installation rocks are built against.
// Executable is the path to the `tarantool` binary; Prefix and IncludeDir
// come from the caller (typically tt's GetTarantoolPrefix). Version is the
// "x.y.z" string for diagnostic / golden-file headers.
type TarantoolConfig struct {
	Executable string
	Prefix     string
	IncludeDir string
	Version    string
}

// RockspecConfig governs the sandboxed evaluator.
//
//   - Env == nil  → os.getenv pass-through to the host process env (matches
//     upstream luarocks 1:1).
//   - Env != nil  → os.getenv returns Env[name] if present, else nil.
//   - Empty non-nil map → os.getenv always returns nil.
type RockspecConfig struct {
	Env map[string]string
}
