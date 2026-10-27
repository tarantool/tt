package manifest

import (
	"path/filepath"
	"slices"
)

// Build backends.
const (
	backendMake  = "make"
	backendShell = "shell"
	backendC     = "c"
	backendLuaC  = "lua-c"
	backendGo    = "go"
	backendCargo = "cargo"
)

// Component kinds ([components.<name>].kind).
const (
	// KindModule is a component whose files are laid out under its namespace
	// in the rocks tree: Lua sources and compiled modules. It is the default.
	KindModule = "module"
	// KindExecutable is a component whose build makes programs, placed in
	// .rocks/bin/: the external processes of an application.
	KindExecutable = "executable"
)

// Component is a group of files plus an optional build ([components.<name>]).
type Component struct {
	Path         string                `toml:"path"`              // Required.
	Kind         string                `toml:"kind,omitempty"`    // module|executable; "": module.
	Include      []string              `toml:"include,omitempty"` // Default *.lua, *.so.
	Exclude      []string              `toml:"exclude,omitempty"`
	Namespace    *string               `toml:"namespace,omitempty"` // Nil: pkg name; "": flat.
	Dependencies map[string]Dependency `toml:"dependencies,omitempty"`
	Build        *Build                `toml:"build,omitempty"`
}

// IsExecutable reports whether the component builds programs into
// .rocks/bin/ rather than laying files out under its namespace.
func (c Component) IsExecutable() bool {
	return c.Kind == KindExecutable
}

// Build describes the native build of a component ([components.<name>.build]).
// The Hook type is an alias of Build; hooks reuse the same shape but allow only
// the make/shell backends.
type Build struct {
	Backend string            `toml:"backend"` // make|shell|c|lua-c|go|cargo - closed enum.
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

	// go and cargo backends. Package is the Go package to build (default
	// ".") or the cargo package that holds the binary.
	Package string `toml:"package,omitempty"`
	// Static asks for a program that needs no loader and no shared library
	// of the machine it runs on; the build checks that it got one.
	Static *bool `toml:"static,omitempty"`

	// go backend.
	Name    string   `toml:"name,omitempty"` // Default: the component name.
	Tags    []string `toml:"tags,omitempty"`
	Ldflags []string `toml:"ldflags,omitempty"`
	Cgo     *bool    `toml:"cgo,omitempty"` // Nil or false: CGO_ENABLED=0.

	// cargo backend.
	Bin      string   `toml:"bin,omitempty"` // Default: the component name.
	Features []string `toml:"features,omitempty"`
	Profile  string   `toml:"profile,omitempty"` // Default: release.
}

// DefaultCargoProfile is the cargo profile an executable is built with when
// the build names none.
const DefaultCargoProfile = "release"

// StaticValue reports whether the build asks for a static program.
func (b Build) StaticValue() bool {
	return b.Static != nil && *b.Static
}

// CgoValue reports whether a go build may use cgo.
func (b Build) CgoValue() bool {
	return b.Cgo != nil && *b.Cgo
}

// Executables lists the names of the programs the build of the executable
// component named component puts into .rocks/bin/, as far as the manifest
// tells them: the one a go or cargo build makes, named by name or bin or
// else after the component, and, for make and shell, the files output
// lists. A make or shell build that writes into TT_OUTPUT_DIR itself names
// none here: what it made is only known once it has run.
func (b Build) Executables(component string) []string {
	switch b.Backend {
	case backendGo:
		return []string{defaultName(b.Name, component)}
	case backendCargo:
		return []string{defaultName(b.Bin, component)}
	}

	names := make([]string, 0, len(b.Output))

	for _, output := range b.Output {
		name := filepath.Base(output)
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names
}

// defaultName is name, or component when name is not set.
func defaultName(name, component string) string {
	if name == "" {
		return component
	}

	return name
}

// ExecutableComponent finds the executable component that builds the
// program named name: one named so, or one whose build names its program so
// (Build.Executables). It reports the component's name.
func (m *Manifest) ExecutableComponent(name string) (string, bool) {
	for _, component := range sortedKeys(m.Components) {
		comp := m.Components[component]
		if !comp.IsExecutable() {
			continue
		}

		if component == name {
			return component, true
		}

		if comp.Build != nil && slices.Contains(comp.Build.Executables(component), name) {
			return component, true
		}
	}

	return "", false
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

// ImplicitProduct names the product a manifest without [products] gets.
const ImplicitProduct = "default"

// EffectiveProducts returns the products the pipeline acts on: the declared
// ones, or, when [products] is absent, a single implicit product named
// ImplicitProduct that is the default and is built from every component.
//
// A manifest without products is not an edge case but the first shape a
// project has: tt new writes one, and tt package add writes into its
// [dependencies]. Every command has to agree on what that manifest means, or
// one resolves its dependencies into nothing while another refuses to build
// it. Validate still checks only what is declared.
func (m *Manifest) EffectiveProducts() map[string]Product {
	if len(m.Products) > 0 {
		return m.Products
	}

	return map[string]Product{
		ImplicitProduct: {Components: sortedKeys(m.Components), Default: true},
	}
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
