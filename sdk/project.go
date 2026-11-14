package sdk

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

// DependencyChange is what [Project.AddDependency] did.
type DependencyChange struct {
	// Existed reports that the table the dependency went to declared the
	// name already.
	Existed bool
	// Previous is the version constraint the declaration carried before,
	// when Existed.
	Previous string
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

// Manifest is what the app.manifest.toml of a project declares, as it
// declares it ([Project.Manifest]): the paths are as they are written,
// relative to the project. It is a copy: changing it changes nothing.
type Manifest struct {
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
	// Registries are the rock servers the lock is resolved with afterwards,
	// as [Dependency.Registries] are.
	Registries []string
}

// ComponentBuild is the build block of a [Component].
type ComponentBuild struct {
	// Backend is the backend that builds the component. Required.
	Backend string
	// Fields are the other fields of the block, in the order they are
	// written after backend: the fields every block has, such as cwd and
	// incremental, and those of the backend.
	Fields []BuildField
}

// BuildField is a field of a build block: its key and its value, a string,
// a bool, an int or a []string.
type BuildField struct {
	Key   string
	Value any
}

// ComponentChange is what [Project.AddComponent] did.
type ComponentChange struct {
	// Declared reports that the manifest was written with the component. It
	// is set with the error of a resolution of the lock that failed too: the
	// component stays declared.
	Declared bool
	// Moves are the rocks whose locked version changed, as
	// [DependencyChange.Moves] are.
	Moves []DependencyMove
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
