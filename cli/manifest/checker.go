package manifest

// BuildChecker checks the build blocks of a manifest for their backends:
// the fields of a backend, which this package leaves in Build.Rest, and what
// a backend asks of the common fields of its block and of the component.
//
// The backends are those of the tt that reads the manifest, its core's and
// its modules', which this package does not know: cli/manifest/buildreg
// holds them and implements the interface. Validate takes one.
type BuildChecker interface {
	// CheckBuild checks build, the build block at field ("components.api.build",
	// "hooks.pre_build"), which builds target. It reports whether a backend
	// of the name of build.Backend is there - the block of none is not
	// checked - and the fields of the block no backend has, by their path
	// from the block, which Validate reports by the format-version rules.
	// An error is a *ValidationError naming the field, by its path from the
	// root of the manifest.
	CheckBuild(field string, target BuildTarget, build Build) (BuildCheck, error)
}

// BuildTarget is what a build block builds: a component, or a hook.
type BuildTarget struct {
	// Component is the name of the component; empty for a hook.
	Component string
	// Kind is the kind of the component, KindModule for one that names
	// none; empty for a hook.
	Kind string
	// Path is the directory of the component, as the manifest gives it.
	Path string
	// Namespace is the namespace the files of a module component are laid
	// out under (Component.EffectiveNamespace); empty for a flat layout, an
	// executable component and a hook.
	Namespace string
	// Package is the name of the package.
	Package string
	// Hook is set for the block of a hook.
	Hook bool
}

// BuildCheck is what a BuildChecker found of a build block besides an error.
type BuildCheck struct {
	// Known reports whether the checker has the backend of the block.
	Known bool
	// Unknown are the fields of the block no backend has, by their path
	// from the block, in the order they are reported in.
	Unknown []string
}
