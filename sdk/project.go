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

// InstallOptions are how [Project.Install] installs the rocks of a project.
type InstallOptions struct {
	// Registries are the rock servers to install from, as --registry of
	// tt package fetch gives them, and replace the other lists as
	// [Dependency.Registries] do. A rock declared with a registry of its own
	// comes from that registry.
	Registries []string
}
