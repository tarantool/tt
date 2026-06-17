package manifest

// Build backends.
const (
	backendMake  = "make"
	backendShell = "shell"
	backendC     = "c"
	backendLuaC  = "lua-c"
)

// Component is a group of files plus an optional build ([components.<name>]).
type Component struct {
	Path         string                `toml:"path"`              // Required.
	Include      []string              `toml:"include,omitempty"` // Default *.lua, *.so.
	Exclude      []string              `toml:"exclude,omitempty"`
	Namespace    *string               `toml:"namespace,omitempty"` // Nil: pkg name; "": flat.
	Dependencies map[string]Dependency `toml:"dependencies,omitempty"`
	Build        *Build                `toml:"build,omitempty"`
}

// Build describes the native build of a component ([components.<name>.build]).
// The Hook type is an alias of Build; hooks reuse the same shape but allow only
// the make/shell backends.
type Build struct {
	Backend string            `toml:"backend"` // make|shell|c|lua-c - closed enum.
	Cwd     string            `toml:"cwd,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
	Output  []string          `toml:"output,omitempty"`

	// make backend.
	MakeTarget string   `toml:"make_target,omitempty"`
	Entrypoint string   `toml:"entrypoint,omitempty"`
	Flags      []string `toml:"flags,omitempty"`

	// shell backend.
	Command string   `toml:"command,omitempty"`
	Args    []string `toml:"args,omitempty"`

	// c / lua-c backends.
	Module      string                  `toml:"module,omitempty"`
	Sources     []string                `toml:"sources,omitempty"`
	IncludeDirs []string                `toml:"include_dirs,omitempty"`
	Libraries   []string                `toml:"libraries,omitempty"`
	LibraryDirs []string                `toml:"library_dirs,omitempty"`
	Defines     []string                `toml:"defines,omitempty"`
	Platforms   map[string]BuildOverlay `toml:"platforms,omitempty"`
}

// BuildOverlay is the per-OS addition to a c/lua-c build
// ([components.<name>.build.platforms.<os>]). It only adds to the base lists
// for the current OS.
type BuildOverlay struct {
	IncludeDirs []string `toml:"include_dirs,omitempty"`
	Libraries   []string `toml:"libraries,omitempty"`
	LibraryDirs []string `toml:"library_dirs,omitempty"`
	Defines     []string `toml:"defines,omitempty"`
}

// Product is a named set of components built and packed as a unit
// ([products.<name>]).
type Product struct {
	Components []string `toml:"components"` // Required; names must exist in [components].
	Default    bool     `toml:"default,omitempty"`
}

// Hook is a lifecycle hook ([hooks.pre_build]/[hooks.post_build]). It shares
// the Build shape, but only the make/shell backends are valid and there is no
// module/sources.
type Hook = Build

// EffectiveNamespace returns the install namespace of the component given the
// package name. An unset namespace falls back to the package name; an explicit
// empty string means a flat layout.
func (c Component) EffectiveNamespace(packageName string) string {
	if c.Namespace == nil {
		return packageName
	}

	return *c.Namespace
}
