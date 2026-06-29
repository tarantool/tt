package rocks

// Field-only data types. Methods belong in the subsystem that owns each
// type's behavior (manif, rockspec, deps, …). Nothing here imports
// gopher-lua; the rockspec evaluator harvests a *Rockspec by populating
// these plain Go fields.

// Rockspec is the parsed and validated contents of a `.rockspec` file.
// `name-version-revision.rockspec` filenames serialize Package, Version
// joined by `-`.
type Rockspec struct {
	// Package is the rock name.
	Package string
	// Version is the version-revision string (e.g. "1.0-1").
	Version string
	// RockspecFormat is the declared rockspec_format (e.g. "3.0"), if any.
	RockspecFormat string
	// Description mirrors the rockspec description table.
	Description Description
	// SupportedPlatforms restricts the rock to the listed platforms, if set.
	SupportedPlatforms []string
	// Dependencies are the runtime dependencies.
	Dependencies []Dep
	// BuildDependencies are needed only to build the rock.
	BuildDependencies []Dep
	// ExternalDependencies are non-Lua libraries/headers, keyed by symbolic name.
	ExternalDependencies map[string]ExternalDep
	// TestDependencies are needed only to run the rock's tests.
	TestDependencies []Dep
	// Source describes where the rock's source is fetched from.
	Source Source
	// Build describes how the rock is built and installed.
	Build Build
}

// Description mirrors the rockspec `description = {...}` table.
type Description struct {
	Summary    string
	Detailed   string
	License    string
	Homepage   string
	IssuesURL  string
	Maintainer string
	Labels     []string
}

// Source mirrors the rockspec `source = {...}` table.
type Source struct {
	// URL drives Fetcher selection (http/git/file).
	URL string
	// Tag is the git tag to check out (git-only).
	Tag string
	// Branch is the git branch to check out (git-only).
	Branch string
	// MD5 is the expected archive checksum, verified for http downloads.
	MD5 string
	// File overrides the downloaded archive's basename (else derived from URL).
	File string
	// Dir overrides the directory the archive is expected to unpack into.
	Dir string
	// Module is the legacy SCM module name (cvs/svn); rarely used.
	Module string
}

// Build mirrors the rockspec `build = {...}` table.
//
// Type ∈ {"builtin","cmake","make","command","none"}. Anything else is
// ErrUnsupportedRockspecFeature.
//
// Platforms contains per-platform Build overrides that the platforms-merge
// step (rockspec/platforms.go) folds into the top-level fields least-
// specific-to-most-specific. After Eval, Platforms is empty.
type Build struct {
	Type    string
	Modules map[string]Module
	Install BuildInstall

	CopyDirectories []string

	// Variables — cmake -D pass-through (cmake backend only).
	Variables map[string]string

	// make backend.
	BuildTarget      string
	BuildVariables   map[string]string
	InstallTarget    string
	InstallVariables map[string]string

	// command backend.
	BuildCommand   string
	InstallCommand string

	Platforms map[string]Build
}

// Module is a single entry in the rockspec `build.modules` table.
//
// String-form module `["foo.bar"] = "src/foo/bar.lua"`: Path != "".
// Table-form module `["foo.bar"] = {sources = {...}, ...}`: Sources non-empty.
type Module struct {
	Path      string
	Sources   []string
	Incdirs   []string
	Libdirs   []string
	Libraries []string
	Defines   []string
}

// BuildInstall mirrors the rockspec `build.install = {...}` table.
// Each map is destination-name → source-path-relative-to-rockspec-dir.
type BuildInstall struct {
	Lua  map[string]string
	Lib  map[string]string
	Bin  map[string]string
	Conf map[string]string
}

// Dep is one entry in the rockspec `dependencies` (or build_dependencies)
// list. Name is the dependency rock name; Constraints is the AND'd set of
// version constraints from "foo >= 1, < 2".
type Dep struct {
	Name        string
	Constraints []VersionConstraint
}

// ExternalDep is one entry in the rockspec `external_dependencies` table:
// Header is e.g. "tarantool/module.h"; Library is e.g. "tarantool".
type ExternalDep struct {
	Header  string
	Library string
}

// Version is a parsed rock version.
//
// Components holds the numeric dot-separated parts ("1.2.3" → [1, 2, 3]).
// Revision is the trailing "-N" (defaults to 0 if absent).
// IsSCM is true for "scm-N"; IsDev is true for "dev-N". Both sort ABOVE
// numeric releases per upstream `core/vers.compare_versions`.
// Raw is the original string for round-trip serialization (e.g. into
// rock_manifest filenames).
type Version struct {
	Raw        string
	Components []int
	Revision   int
	IsSCM      bool
	IsDev      bool
}

// VersionConstraint is one operator+version pair from a constraint
// expression. Op ∈ {"==", "~=", ">", "<", ">=", "<=", "~>"}.
type VersionConstraint struct {
	Op      string
	Version Version
}

// Manifest is the top-level tree manifest at `<tree>/manifest`. The
// upstream format is a Lua-source serialization of a table.
type Manifest struct {
	// Repository indexes installed rocks: name → version → per-arch entry.
	Repository map[string]map[string]RepoEntry
	// Modules maps a module name to the "name/version" rocks that provide it.
	Modules map[string][]string
	// Commands maps a command-binary name to the "name/version" rocks that
	// provide it.
	Commands map[string][]string
	// Dependencies records each rock's resolved deps: name → version → deps.
	Dependencies map[string]map[string][]Dep
}

// RepoEntry is one entry under `repository.<name>.<version>`. Arch is
// typically "installed" for a Tarantool tree.
//
// Modules and Commands carry the per-arch index that upstream luarocks
// emits inside each repository entry: module name → on-disk slashed path
// for Modules, command-binary name → relative install path for Commands.
// Populated by the facade after `tree.Deploy` returns the *RockManifest*
// for the installed rock. Empty for a freshly-installed entry only if the
// rock genuinely has no modules / commands.
type RepoEntry struct {
	Arch     string
	Modules  map[string]string
	Commands map[string]string
}

// VersionedRock is one row from a RemoteIndex.Query result: a (name,
// version) tuple and the URL of the resource (.rock or .rockspec) on the
// originating server. Spec, if non-nil, is the preloaded rockspec for
// that version — the resolver uses it instead of re-fetching when present.
type VersionedRock struct {
	Name    string
	Version Version
	URL     string
	Spec    *Rockspec
}

// InstallStep is one entry in the topo-ordered install list produced by
// deps.Resolve. Steps appear deepest-dep-first: a step's prerequisites
// all precede it. The facade's Install method walks this list in order.
type InstallStep struct {
	Name     string
	Version  Version
	URL      string
	Rockspec *Rockspec
}

// InstalledRock is one row of the result returned by Rocks.List. Version
// is the raw display string matching what `luarocks list` would print.
type InstalledRock struct {
	Name    string
	Version string
}

// ShowInfo is the projection returned by Rocks.Show — the human-readable
// summary upstream's `luarocks show` prints.
type ShowInfo struct {
	Package      string
	Version      string
	Summary      string
	License      string
	Homepage     string
	Modules      []string
	Dependencies []Dep
}

// RockManifest is the per-rock manifest at
// `<tree>/share/tarantool/rocks/<name>/<ver>/rock_manifest`.
// Each map is path → md5 hex.
type RockManifest struct {
	Rockspec string
	Lua      map[string]string
	Lib      map[string]string
	Bin      map[string]string
	Conf     map[string]string
	Doc      map[string]string
}
