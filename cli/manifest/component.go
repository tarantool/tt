package manifest

import (
	"fmt"
	"maps"
	"slices"
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

// Build describes the native build of a component ([components.<name>.build])
// or a hook ([hooks.<name>]).
//
// The fields of the struct are those of every build block, whatever its
// backend. The fields of the backend are left in Rest as the TOML decoder
// reads them, for the backend to decode and check: the backends are those
// the core and the modules of a tt provide, which this package does not
// know (Validate is given a BuildChecker that does).
type Build struct {
	// Backend names the backend that builds the block.
	Backend string `toml:"backend"`
	// Cwd is the working directory of the build, relative to the project.
	Cwd string `toml:"cwd,omitempty"`
	// Env are the variables the build block adds to the environment.
	Env map[string]string `toml:"env,omitempty"`
	// Output are the files the build makes, for a backend that copies them.
	Output []string `toml:"output,omitempty"`
	// Incremental skips the build of a component when its inputs - the
	// build block and the files of the component, but those its .gitignore
	// files ignore - are the ones of its last build, and its outputs are
	// there; for a backend that allows it.
	Incremental *bool `toml:"incremental,omitempty"`
	// Static, of the build of any component, asks for what the build makes
	// to need nothing of the machine it is installed on, and the build checks
	// that it does. A program, of an executable component, needs no loader
	// and no shared library: go and cargo are asked to build one so, and what
	// cmake, make or shell built is only checked. A module needs no shared
	// library but those of the C library and the loader, which Tarantool,
	// which loads it, has loaded itself; its backend is asked for nothing.
	Static *bool `toml:"static,omitempty"`

	// Rest are the other fields of the block, those of its backend, by key,
	// as the TOML decoder reads them: tables are map[string]any, arrays
	// []any, numbers int64 and float64. Nil when there are none.
	Rest map[string]any `toml:"-"`
}

// commonBuildFields are the keys of the fields every build block has, those
// of the Build struct: the rest of a block go to Build.Rest.
var commonBuildFields = []string{"backend", "cwd", "env", "output", "incremental", "static"}

// CommonBuildFields returns the keys of the fields every build block has,
// which no backend may have as its own.
func CommonBuildFields() []string {
	return slices.Clone(commonBuildFields)
}

// IncrementalValue reports whether the build is skipped when its inputs did
// not change.
func (b Build) IncrementalValue() bool {
	return b.Incremental != nil && *b.Incremental
}

// StaticValue reports whether the build asks for a static program or module
// (Static).
func (b Build) StaticValue() bool {
	return b.Static != nil && *b.Static
}

// Canonical returns the build block in the canonical form of manifest_hash
// (HashTOML), its fields and those of Rest alike: the same block whatever
// its spelling in the file, and another one for any change of a value.
func (b Build) Canonical() ([]byte, error) {
	form, err := canonicalForm(b.tree())
	if err != nil {
		return nil, fmt.Errorf("hashing the build block: %w", err)
	}

	return form, nil
}

// tree returns the build block as the TOML decoder reads one into a generic
// tree: its fields, which an empty value leaves out as the struct does, and
// those of Rest.
func (b Build) tree() map[string]any {
	tree := make(map[string]any, len(commonBuildFields)+len(b.Rest))

	maps.Copy(tree, b.Rest)

	tree["backend"] = b.Backend

	if b.Cwd != "" {
		tree["cwd"] = b.Cwd
	}

	if len(b.Env) > 0 {
		env := make(map[string]any, len(b.Env))
		for key, value := range b.Env {
			env[key] = value
		}

		tree["env"] = env
	}

	if len(b.Output) > 0 {
		output := make([]any, 0, len(b.Output))
		for _, entry := range b.Output {
			output = append(output, entry)
		}

		tree["output"] = output
	}

	if b.Incremental != nil {
		tree["incremental"] = *b.Incremental
	}

	if b.Static != nil {
		tree["static"] = *b.Static
	}

	return tree
}

// ExecutableComponent finds the executable component that builds the
// program named name: one named so, or one whose build names its program so
// to checker (BuildCheck.Programs); a nil checker knows no program. It
// reports the component's name.
func (m *Manifest) ExecutableComponent(checker BuildChecker, name string) (string, bool) {
	for _, component := range sortedKeys(m.Components) {
		comp := m.Components[component]
		if !comp.IsExecutable() {
			continue
		}

		if component == name {
			return component, true
		}

		if comp.Build == nil || checker == nil {
			continue
		}

		check, err := checker.CheckBuild("components."+component+".build",
			m.ComponentTarget(component), *comp.Build)
		if err == nil && slices.Contains(check.Programs, name) {
			return component, true
		}
	}

	return "", false
}

// Product is a named set of components built and packed as a unit
// ([products.<name>]).
type Product struct {
	Components []string `toml:"components"` // Required; names must exist in [components].
	Default    bool     `toml:"default,omitempty"`
	// Rock is the name of the rock the product is released as (ProductRock);
	// empty is the package name for the default product, and no rock of its
	// own for another.
	Rock string `toml:"rock,omitempty"`
}

// ProductRock returns the name of the rock the product name is released as
// and other projects depend on: the rock it names, or the package name for
// the default product. A product that is not the default and names none has
// no rock of its own, and the name is "".
func (m *Manifest) ProductRock(name string) string {
	product, ok := m.EffectiveProducts()[name]
	if !ok {
		return ""
	}

	if product.Rock != "" {
		return product.Rock
	}

	if defaultName, _, ok := m.DefaultProduct(); ok && defaultName == name {
		return m.Package.Name
	}

	return ""
}

// RockProduct returns the product released as the rock named rock
// (ProductRock), and whether there is one.
func (m *Manifest) RockProduct(rock string) (string, bool) {
	for _, name := range sortedKeys(m.EffectiveProducts()) {
		if m.ProductRock(name) == rock {
			return name, true
		}
	}

	return "", false
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

// DefaultProduct returns the product a command acts on when it is not named
// one, among the effective products: the only one, or the one marked default.
// It reports false when there are several and none is marked, which Validate
// rejects.
func (m *Manifest) DefaultProduct() (string, Product, bool) {
	products := m.EffectiveProducts()

	names := sortedKeys(products)
	if len(names) == 1 {
		return names[0], products[names[0]], true
	}

	for _, name := range names {
		if products[name].Default {
			return name, products[name], true
		}
	}

	return "", Product{}, false
}

// Hook is a lifecycle hook ([hooks.pre_build]/[hooks.post_build]). It is a
// build block, but only a backend that runs hooks runs one (make and shell
// of the backends of tt), it has no component and static is refused.
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
