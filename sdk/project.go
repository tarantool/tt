package sdk

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/tarantool/tt/sdk/build"
)

// errOtherBackend reports a build block ManifestBuild.Decode is given the
// backend of another block.
var errOtherBackend = errors.New("the block is of another backend")

// Dependency is a rock to declare in the manifest of a project
// ([Project.AddDependency]).
type Dependency struct {
	// Name is the name of the rock.
	Name string
	// Constraint is the version constraint to declare, such as ">=1.2.0";
	// "" declares "*", whatever the rock servers serve.
	Constraint string
	// Registry is the rock server the rock is to come from, a URL or a
	// directory relative to the project directory. With one the rock is
	// declared in the long form, name = { version = ..., registry = ... },
	// and resolved and installed from that server alone; "" declares the
	// short form, name = constraint, which comes from the rock servers of
	// the project.
	Registry string
	// Dev declares the rock in [dev_dependencies], what the project is
	// developed and tested with, instead of [dependencies].
	Dev bool
	// Registries are the rock servers to resolve with, as --registry of
	// tt package add gives them: URLs or directories relative to the project
	// directory, asked in order. When there are any they replace the
	// servers TT_REGISTRIES, the [platform].registries of the manifest and
	// the defaults name; with none, those are used, as tt package add uses
	// them. The Registry of a declaration outranks every list.
	Registries []string
}

// DependencyChange is what [Project.AddDependency] and
// [Project.RemoveDependency] did.
type DependencyChange struct {
	// Existed reports that the table the dependency went to declared the
	// name already; for a removal, that it was declared, which it is to be.
	Existed bool
	// Previous is the version constraint the declaration carried before,
	// when Existed.
	Previous string
	// PreviousRegistry is the registry the declaration named before, when
	// Existed: "" for one that named none.
	PreviousRegistry string
	// Moves are the rocks whose locked version changed, across the closures
	// of every product and the development closure, in name order. They
	// are versions, not constraints: a declaration the lock satisfied
	// already moves nothing.
	Moves []DependencyMove
}

// DependencyMove is the change of one rock in the lock of a project.
type DependencyMove struct {
	// Name is the name of the rock.
	Name string
	// From is the version the lock held before, "" for a rock it did not
	// hold.
	From string
	// To is the version the lock holds now, "" for a rock it dropped.
	To string
}

// String renders the move as tt package reports one, a line each: a rock
// that arrived with its version, "name 1.0.0-1"; one that dropped out
// marked so, "name 1.0.0-1 -> (dropped)"; one that moved with both ends,
// "name 1.0.0-1 -> 1.1.0-1".
func (m DependencyMove) String() string {
	switch {
	case m.From == "":
		return m.Name + " " + m.To
	case m.To == "":
		return m.Name + " " + m.From + " -> (dropped)"
	default:
		return m.Name + " " + m.From + " -> " + m.To
	}
}

// RegistryFlagUsage is the help of the --registry flag of tt package: for
// the flag of a command of a module that takes the rock servers to resolve
// or install with ([Dependency.Registries]), so that it reads as tt's.
const RegistryFlagUsage = "rock server to use instead of the configured ones: " +
	"an http(s) URL or a local directory; repeat to give an ordered list " +
	"(overrides TT_REGISTRIES and [platform].registries)"

// ResolveOptions are how [Project.Resolve] and [Project.RemoveDependency]
// resolve the lock of a project.
type ResolveOptions struct {
	// Registries are the rock servers to resolve with, as
	// [Dependency.Registries] are.
	Registries []string
}

// ResolveChange is what [Project.Resolve] did.
type ResolveChange struct {
	// Moves are the rocks whose locked version changed, as
	// [DependencyChange.Moves] are.
	Moves []DependencyMove
}

// Manifest is what the app.manifest.toml of a project declares, as it
// declares it ([Project.Manifest]): the paths are as they are written,
// relative to the project. It is a copy: changing it changes nothing.
type Manifest struct {
	// Package is the name of the package, [package].name.
	Package string
	// Dependencies are the rocks of [dependencies], by name.
	Dependencies map[string]ManifestDependency
	// DevDependencies are the rocks of [dev_dependencies], by name.
	DevDependencies map[string]ManifestDependency
	// Components are the components of [components], by name.
	Components map[string]ManifestComponent
}

// ManifestDependency is a rock a manifest declares.
type ManifestDependency struct {
	// Version is the version constraint, such as ">=1.2.0" or "0.1.0"; ""
	// for a path dependency.
	Version string
	// Registry is the rock server the rock comes from, a URL or a directory
	// relative to the project; "" for one that comes from the rock servers
	// of the project.
	Registry string
	// Path is the directory of a path dependency, relative to the project;
	// "" for a rock of a rock server.
	Path string
}

// ManifestComponent is a component a manifest declares.
type ManifestComponent struct {
	// Path is the directory of the component, relative to the project.
	Path string
	// Include and Exclude are the patterns of what of the directory the
	// component lays out into the package, as they are written; none for
	// the default.
	Include []string
	Exclude []string
	// Namespace is where the files and the modules of the component go in
	// the package, under .rocks/share/tarantool/ and .rocks/lib/tarantool/:
	// the package name, unless the manifest names another, and "" for the
	// top level.
	Namespace string
	// Build is its build block; nil for a component that builds nothing.
	Build *ManifestBuild
}

// ManifestBuild is the build block of a component a manifest declares.
type ManifestBuild struct {
	// Backend is the backend that builds the component.
	Backend string
	// Cwd is the working directory of the build, relative to the project;
	// "" for the directory of the component.
	Cwd string
	// Env are the variables the block adds to the environment of the build.
	Env map[string]string
	// Output are the files of output, in their order: one written alone,
	// output = "dist/calc.wasm", is the one entry.
	Output []ManifestOutput
	// Incremental and Static are the incremental and static of the block,
	// false when it says nothing.
	Incremental bool
	Static      bool
	// Fields are the other fields of the block, those of its backend, by
	// key, as the TOML decoder reads them: a table is a map[string]any, an
	// array a []any, an integer an int64 and a float a float64. Nil when
	// there are none. [ManifestBuild.Decode] decodes them as the core does.
	Fields map[string]any
}

// ManifestOutput is an entry of the output of a build block.
type ManifestOutput struct {
	// Path is the file, relative to where the backend takes it from: the
	// working directory of the build for a backend whose output the core
	// copies ([build.OutputCwd]).
	Path string
	// To is where the entry says the file goes: "lib", "share" or "bin",
	// and "" for an entry that says nothing, whose place is that of the one
	// type of a backend that produces one, and lib otherwise
	// ([build.OutputFile.To]).
	To string
}

// Decode decodes the fields of the block, [ManifestBuild.Fields], into a
// fresh configuration of backend with the decoder of the core
// ([build.Backend.Decode]): what the core gives backend.New when it builds
// the component, for a command of the module of backend that reads the
// block it builds, such as tt wasm status. It is strict: a field the
// configuration has no place for is an error, a [*build.FieldError] naming
// it, where the core warns of one that a newer manifest format may have. A
// block of another backend is an error too.
func (b ManifestBuild) Decode(backend build.Backend) (any, error) {
	if b.Backend != backend.Name {
		return nil, fmt.Errorf("%w: %q, not %q", errOtherBackend, b.Backend, backend.Name)
	}

	cfg, unknown, err := backend.Decode(b.Fields)
	if err != nil {
		return nil, err
	}

	if len(unknown) > 0 {
		return nil, build.FieldErrorf(unknown[0], "unknown field of backend %q", backend.Name)
	}

	return cfg, nil
}

// clone returns a copy of b that shares no map or slice with it, the values
// of Fields included.
func (b ManifestBuild) clone() ManifestBuild {
	b.Env = maps.Clone(b.Env)
	b.Output = slices.Clone(b.Output)

	if b.Fields != nil {
		fields, _ := cloneValue(b.Fields).(map[string]any)

		b.Fields = fields
	}

	return b
}

// Clone returns a copy of m that shares no map or slice with it: a module
// may change it as it likes.
func (m Manifest) Clone() Manifest {
	clone := Manifest{
		Package:         m.Package,
		Dependencies:    maps.Clone(m.Dependencies),
		DevDependencies: maps.Clone(m.DevDependencies),
		Components:      make(map[string]ManifestComponent, len(m.Components)),
	}

	for name, component := range m.Components {
		component.Include = slices.Clone(component.Include)
		component.Exclude = slices.Clone(component.Exclude)

		if component.Build != nil {
			build := component.Build.clone()

			component.Build = &build
		}

		clone.Components[name] = component
	}

	return clone
}

// cloneValue returns a deep copy of a value of a TOML tree: maps and slices
// are copied, other values are returned as they are.
func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = cloneValue(item)
		}

		return out
	case []any:
		out := make([]any, 0, len(value))
		for _, item := range value {
			out = append(out, cloneValue(item))
		}

		return out
	default:
		return value
	}
}

// Component is a component to declare in the manifest of a project
// ([Project.AddComponent]).
type Component struct {
	// Name is the name of the component in the manifest. Required.
	Name string
	// Comment, when not empty, is written above the table of the component,
	// a "# " line for each of its lines: what made the component.
	Comment string
	// Path is the directory of the component, relative to the project.
	// Required.
	Path string
	// Include is what of the directory the component lays out into the
	// package; none declares none, which lays out the default.
	Include []string
	// Build is the build block of the component; nil declares none.
	Build *ComponentBuild
}

// ComponentBuild is the build block of a [Component].
type ComponentBuild struct {
	// Backend is the backend that builds the component. Required.
	Backend string
	// Fields are the other fields of the block, in the order they are
	// written after backend: the fields every block has, such as cwd,
	// output and incremental, and those of the backend.
	Fields []BuildField
}

// BuildField is a field of a table of a manifest a project writes: its key
// and its value, a string, a bool, an integer, a slice of them or a map
// with string keys of them, written on one line - a map as an inline table
// in the order of its keys. output = "dist/calc.wasm" is
// {Key: "output", Value: "dist/calc.wasm"}, and
// {Key: "output", Value: []map[string]string{{"path": "build/tool", "to": "bin"}}}
// writes output = [{ path = "build/tool", to = "bin" }].
type BuildField struct {
	Key   string
	Value any
}

// ComponentUpdate is a change of a component the manifest of a project
// declares ([Project.UpdateComponent]).
type ComponentUpdate struct {
	// Name is the name of the component. Required.
	Name string
	// Fields are fields of [components.<Name>] to write, such as path,
	// include and namespace: a field there has its value replaced where it
	// is written, another is added after the last field of the table.
	Fields []BuildField
	// Build are fields of its build block to write, as Fields are; a
	// component without a build block gets one, which is then to name its
	// backend.
	Build []BuildField
	// Remove are the keys to remove first: a field of the component
	// ("include"), a field of its build block ("build.cwd"), or the build
	// block with its tables ("build"). A key the component does not have
	// is no error.
	Remove []string
}

// LockFile is what the app.manifest.lock of a project pins
// ([Project.ReadLock]).
type LockFile struct {
	// Fresh reports that the lock is that of the manifest as it is: a
	// build of the project resolves nothing, and tt package build --locked
	// takes it.
	Fresh bool
	// Reason says why the lock is not fresh, "" when it is.
	Reason string
	// DefaultProduct is the product a build builds when it is not named
	// one.
	DefaultProduct string
	// Products are the closures of the products, by product, each the
	// rocks it pins by name.
	Products map[string]map[string]LockedRock
	// DevDependencies is the closure of [dev_dependencies], by name.
	DevDependencies map[string]LockedRock
}

// LockedRock is a rock a lock pins.
type LockedRock struct {
	// Version is the exact version, such as "0.1.0-1".
	Version string
	// Path is the directory of a path dependency, as the lock has it; ""
	// for a rock of a rock server.
	Path string
}

// Locked returns the rock named name as the closure of the default product
// pins it, or the development closure when that one does not, and whether
// either does: what [build.Context.LockedVersion] answers in a build of the
// default product.
func (l LockFile) Locked(name string) (LockedRock, bool) {
	if rock, ok := l.Products[l.DefaultProduct][name]; ok {
		return rock, true
	}

	rock, ok := l.DevDependencies[name]

	return rock, ok
}

// BuildOptions are what [Project.Build] builds, as the flags of
// tt package build choose it.
type BuildOptions struct {
	// Product is the product to build; "" for the default one.
	Product string
	// Components narrow the build to these components of the product,
	// built in its order; none builds every component of the product. tt
	// package build NAME is Components: []string{"NAME"}.
	Components []string
	// Locked fails the build on a lock that is stale, instead of resolving
	// it again, as --locked does.
	Locked bool
	// Rebuild builds the incremental components whatever they recorded,
	// each from an empty TT_BUILD_DIR, as --rebuild does.
	Rebuild bool
	// Registries are the rock servers to resolve and install with, as
	// [Dependency.Registries] are.
	Registries []string
	// ShowOutput shows what the tools of the builds print as they run, as
	// tt -V does, which shows it anyway; without it what a tool printed is
	// shown when it fails.
	ShowOutput bool
	// Args are passed through to the build of the one component
	// Components names, as [build.Context.Args], for its tool: what follows
	// -- of tt wasm build calc -- -opt=2. With any, Components is to name
	// exactly one component.
	Args []string
}

// BuildResult is what [Project.Build] built.
type BuildResult struct {
	// Product is the product built, the default one selected.
	Product string
	// Version is the version of the package, as TT_VERSION gives it.
	Version string
	// Components are the components with a build block the build built or
	// found up to date, by name.
	Components map[string]BuiltComponent
}

// BuiltComponent is what the build of a component made, by type, as
// absolute paths in .rocks of the project, where the build installed it:
// the modules in lib/tarantool/<namespace>/, the data files in
// share/tarantool/<namespace>/, the programs in bin/.
type BuiltComponent struct {
	Modules  []string
	Data     []string
	Programs []string
	// UpToDate reports an incremental build that was skipped: its inputs
	// were those of its last build, and what that made was there.
	UpToDate bool
}

// BuildContextOptions are the build whose Context [Project.BuildContext]
// returns.
type BuildContextOptions struct {
	// Product is the product the component is built for; "" for the
	// default one.
	Product string
	// Component is the component. Required: it is to have a build block.
	Component string
	// Locked is [build.Context.Locked].
	Locked bool
	// Args are [build.Context.Args].
	Args []string
}

// CleanOptions are what [Project.Clean] cleans, as tt package clean chooses
// it.
type CleanOptions struct {
	// Components narrow the clean to these components of the manifest; none
	// cleans the whole project. tt wasm clean calc is
	// Components: []string{"calc"}.
	Components []string
	// DryRun removes nothing and runs no Clean of a backend and no hook, as
	// --dry-run does: the result says what a clean would remove and run.
	DryRun bool
}

// CleanResult is what [Project.Clean] removed and ran, or would have with
// [CleanOptions.DryRun].
type CleanResult struct {
	// Removed are the files and directories removed, as absolute paths:
	// those of _build/, then the files of the rock of the package in
	// .rocks/.
	Removed []string
	// Cleaned are the components whose backend's Clean ran
	// ([build.Cleaner]), in name order.
	Cleaned []string
	// Hook reports that [hooks.clean] of the manifest ran.
	Hook bool
}

// IndexOptions are how [Rocks.Index] indexes a rock server.
type IndexOptions struct {
	// IfIndexed writes the rock index only into a directory that has one
	// already, keeping it current; a directory without one stays a server
	// of LuaRocks manifests alone. That suits a server of local builds: a
	// rock index would have the lock of a project pin the sha256 of a rock
	// packed anew from every build.
	IfIndexed bool
}

// InstallOptions are how [Project.Install] installs the rocks of a project.
type InstallOptions struct {
	// Registries are the rock servers to install from, as --registry of
	// tt package fetch gives them, and replace the other lists as
	// [Dependency.Registries] do. A rock declared with a registry of its own
	// comes from that registry.
	Registries []string
}
