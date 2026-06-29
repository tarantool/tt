package client

// This file holds the option/result types for the thirteen Engine write
// operations the native backend does not implement. Fields are modeled from
// the upstream luarocks/cmd/<name>.lua argparse blocks (cmd:flag → Go bool,
// cmd:option → Go string, cmd:argument that varies → method parameter).
//
// Intentionally lean: only the commonly useful flags are surfaced, not every
// hidden/debug option. For every field, the Go zero value (false / "" / nil)
// maps to the upstream default — i.e. the flag/option is simply omitted from
// the argv when the field is unset.

// RemoveOpts tunes Remove (luarocks remove). See cmd/remove.lua.
type RemoveOpts struct {
	// Version, if set, restricts removal to that exact version; empty removes
	// all versions of the rock (the `version` positional).
	Version string
	// Force maps to --force: remove even if it would break dependencies.
	Force bool
	// ForceFast maps to --force-fast: forced removal without reporting
	// dependency issues.
	ForceFast bool
	// Deps maps to --deps-mode {all,one,order,none}.
	Deps DepsPolicy
}

// PurgeOpts tunes Purge (luarocks purge). See cmd/purge.lua. Purge always
// operates on the engine's configured tree (the --tree global is mandatory
// upstream and is emitted automatically).
type PurgeOpts struct {
	// OldVersions maps to --old-versions: keep the highest version of each
	// rock and remove the rest.
	OldVersions bool
	// Force maps to --force: with --old-versions, force removal even if it
	// would break dependencies.
	Force bool
	// ForceFast maps to --force-fast: like Force but without dependency
	// reporting.
	ForceFast bool
}

// SearchOpts tunes Search (luarocks search). See cmd/search.lua. The engine
// always passes --porcelain so the listing is machine-parseable; that flag is
// not modeled here.
type SearchOpts struct {
	// Version, if set, is the `version` positional to search for.
	Version string
	// Source maps to --source: return only rockspecs and source rocks.
	Source bool
	// Binary maps to --binary: return only pure-Lua and binary rocks.
	Binary bool
	// All maps to --all: list all suitable contents of the server(s).
	All bool
	// Servers, when non-empty, appends a --server <s> global option per entry.
	Servers []string
}

// SearchResult is a single match returned by Search. It mirrors the
// --porcelain print format of search.print_result_tree:
// "<name>\t<version>\t<arch>\t<repo>\t<namespace>".
type SearchResult struct {
	// Name is the matched rock name.
	Name string
	// Version is the matched version-revision string.
	Version string
	// Server is the repository URL the match came from.
	Server string
}

// DownloadOpts tunes Download (luarocks download). See cmd/download.lua.
type DownloadOpts struct {
	// Version, if set, is the `version` positional.
	Version string
	// All maps to --all: download all files when multiple match.
	All bool
	// Source maps to --source: download the .src.rock if available.
	Source bool
	// Rockspec maps to --rockspec: download the .rockspec if available.
	Rockspec bool
	// Arch maps to --arch <arch>: download for a specific architecture.
	Arch string
	// Servers, when non-empty, appends a --server <s> global option per entry.
	Servers []string
}

// LintOpts tunes Lint (luarocks lint). See cmd/lint.lua. lint takes only the
// rockspec positional, so there are no tunable flags.
type LintOpts struct{}

// NewVersionOpts tunes NewVersion (luarocks new_version). See
// cmd/new_version.lua. The rockspec/name is the method parameter.
type NewVersionOpts struct {
	// NewVersion, if set, is the `new_version` positional.
	NewVersion string
	// NewURL, if set, is the `new_url` positional (requires NewVersion).
	NewURL string
	// Dir maps to --dir <dir>: output directory for the new rockspec.
	Dir string
	// Tag maps to --tag <tag>: new SCM tag.
	Tag string
}

// WriteRockspecOpts tunes WriteRockspec (luarocks write_rockspec). See
// cmd/write_rockspec.lua. The location/url is the method parameter; name and
// version are options here since upstream can infer them.
type WriteRockspecOpts struct {
	// Name, if set, is the `name` positional.
	Name string
	// Version, if set, is the `version` positional.
	Version string
	// Output maps to --output <file>: write the rockspec with this filename.
	Output string
	// License maps to --license <string>.
	License string
	// Summary maps to --summary <txt>.
	Summary string
	// Detailed maps to --detailed <txt>.
	Detailed string
	// Homepage maps to --homepage <txt>.
	Homepage string
	// LuaVersions maps to --lua-versions <ver>.
	LuaVersions string
	// RockspecFormat maps to --rockspec-format <ver>.
	RockspecFormat string
	// Tag maps to --tag <tag>.
	Tag string
	// Lib maps to --lib <libs>: comma-separated C libraries to link to.
	Lib string
}

// DocOpts tunes Doc (luarocks doc). See cmd/doc.lua. Doc is tree-scoped (it
// looks up an installed rock), so the --tree global is emitted automatically.
type DocOpts struct {
	// Version, if set, is the `version` positional.
	Version string
	// Home maps to --home: open the project home page.
	Home bool
	// List maps to --list: list documentation files only.
	List bool
}

// TestOpts tunes Test (luarocks test). See cmd/test.lua. The rockspec is the
// method parameter.
type TestOpts struct {
	// Prepare maps to --prepare: install test deps only, do not run the suite.
	Prepare bool
	// TestType maps to --test-type <type>: select the test suite type.
	TestType string
	// Args are passed through as the trailing `args` positionals to the suite.
	Args []string
}

// ConfigOpts tunes Config (luarocks config). See cmd/config.lua.
type ConfigOpts struct {
	// Key, if set, is the `key` positional: prints that entry's value. Empty
	// prints the whole effective configuration.
	Key string
	// Value, if set, is the `value` positional: writes Key=Value instead of
	// printing.
	Value string
	// Unset maps to --unset: delete Key from the configuration file.
	Unset bool
	// Scope maps to --scope <scope> {system,user,project}.
	Scope string
	// JSON maps to --json: output as JSON.
	JSON bool
}

// UploadOpts tunes Upload (luarocks upload). See cmd/upload.lua. The rockspec
// is the method parameter.
type UploadOpts struct {
	// SrcRock, if set, is the `src-rock` positional (a matching .src.rock).
	SrcRock string
	// SkipPack maps to --skip-pack: do not pack and send the source rock.
	SkipPack bool
	// APIKey maps to --api-key <key>.
	APIKey string
	// TempKey maps to --temp-key <key>.
	TempKey string
	// Force maps to --force: replace an existing rockspec of the same revision.
	Force bool
	// Sign maps to --sign: upload a signature file alongside each file.
	Sign bool
}

// InitProjectOpts tunes InitProject (luarocks init). See cmd/init.lua.
type InitProjectOpts struct {
	// Name, if set, is the `name` positional (the project name). Empty lets
	// upstream derive it from the working directory.
	Name string
	// Version, if set, is the `version` positional.
	Version string
	// Reset maps to --reset: delete and regenerate the project config and
	// ./lua tree.
	Reset bool
}

// AdminOpts tunes Admin (luarocks admin <subcmd>). See luarocks/admin/cmd/*.
// Admin subcommands vary widely; common cross-cutting flags are modeled and
// the rest are passed verbatim via the Admin args parameter.
type AdminOpts struct {
	// Server maps to --server <s>: the server to operate on.
	Server string
	// Force maps to --force where the subcommand supports it.
	Force bool
}
