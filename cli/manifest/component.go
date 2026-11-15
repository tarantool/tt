package manifest

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// The kinds of a component of manifest_version 0.1 ([components.<name>].kind),
// which is deprecated: a component makes what its build makes - modules,
// data files and programs, any of them at once - and lays out its files.
// A kind is still accepted, as what the component says of its build, and the
// build checks it.
const (
	// KindModule says the build of the component makes no program.
	KindModule = "module"
	// KindExecutable says the build of the component makes programs only,
	// placed in .rocks/bin/, and that the component lays out no files.
	KindExecutable = "executable"
)

// Component is a group of files plus an optional build ([components.<name>]).
type Component struct {
	Path string `toml:"path"` // Required.
	// Kind is the deprecated kind of manifest_version 0.1, KindModule or
	// KindExecutable; empty when the manifest says none, as it is to.
	Kind         string                `toml:"kind,omitempty"`
	Include      []string              `toml:"include,omitempty"` // Default *.lua, *.so.
	Exclude      []string              `toml:"exclude,omitempty"`
	Namespace    *string               `toml:"namespace,omitempty"` // Nil: pkg name; "": flat.
	Dependencies map[string]Dependency `toml:"dependencies,omitempty"`
	Build        *Build                `toml:"build,omitempty"`
}

// ProgramsOnly reports whether the component says its build makes programs
// only and lays out no files: kind = "executable" of manifest_version 0.1.
func (c Component) ProgramsOnly() bool {
	return c.Kind == KindExecutable
}

// Where a file of the output of a build block goes (OutputEntry.To).
const (
	// OutputToLib is a module: lib/<namespace>/ of the package, the default.
	OutputToLib = "lib"
	// OutputToShare is a data file: share/<namespace>/ of the package.
	OutputToShare = "share"
	// OutputToBin is a program: bin/ of the package, .rocks/bin/.
	OutputToBin = "bin"
	// OutputToLink is a library, or a header of one, that the components
	// built after it link (the link field of a c or lua-c build): it goes to
	// _build/link/<product>/<component>/, and is not in the package.
	OutputToLink = "link"
)

// The keys of the table form of an entry of output.
const (
	outputPathKey = "path"
	outputToKey   = "to"
)

// OutputEntry is a file of the output of a build block. It is written in two
// forms: a string, the path of the file, and a table,
// { path = "build/tool", to = "bin" }, which says where the file goes.
type OutputEntry struct {
	// Path is the file, relative to where the backend of the block takes
	// it from: the working directory of make and shell, the build tree of
	// cmake. Required.
	Path string `toml:"path"`
	// To is where the file goes: OutputToLib, OutputToShare, OutputToBin or
	// OutputToLink.
	// Empty says nothing, and the backend of the block tells where it goes
	// (buildreg.OutputType): where the one type of result of a backend that
	// makes one goes, bin for a component of kind = "executable", lib
	// otherwise.
	To string `toml:"to,omitempty"`
}

// OutputList is the output field of a build block: an array of entries, or
// one entry written alone, output = "dist/calc.wasm", which is the array of
// that entry.
type OutputList []OutputEntry

// UnmarshalTOML decodes the field in either form: an array of entries, or
// an entry alone, a string or an inline table. The decoder must have
// EnableUnmarshalerInterface set (ParseManifest does). An array of tables
// ([[components.<name>.build.output]]) is decoded table by table into the
// entries instead.
func (l *OutputList) UnmarshalTOML(node *unstable.Node) error {
	switch node.Kind {
	case unstable.String, unstable.InlineTable:
		var entry OutputEntry

		err := entry.UnmarshalTOML(node)
		if err != nil {
			return err
		}

		*l = OutputList{entry}

		return nil
	case unstable.Array:
	default:
		return invalid("", "output must be a file, a table { path = ..., to = ... } "+
			"or an array of them, got %s", strings.ToLower(node.Kind.String()))
	}

	list := OutputList{}

	it := node.Children()
	for it.Next() {
		var entry OutputEntry

		err := entry.UnmarshalTOML(it.Node())
		if err != nil {
			return err
		}

		list = append(list, entry)
	}

	*l = list

	return nil
}

// UnmarshalTOML decodes an entry of either form. The decoder must have
// EnableUnmarshalerInterface set (ParseManifest does). An entry written as
// a table of an array of tables ([[components.<name>.build.output]]) is
// decoded key by key into the struct instead.
func (e *OutputEntry) UnmarshalTOML(node *unstable.Node) error {
	switch node.Kind {
	case unstable.String:
		e.Path = string(node.Data)

		return nil
	case unstable.InlineTable, unstable.Table:
	default:
		return invalid("", "an entry of output must be a string or a table, got %s", node.Kind)
	}

	it := node.Children()
	for it.Next() {
		entry := it.Node()

		keys := entry.Key()
		if !keys.Next() {
			continue
		}

		key := string(keys.Node().Data)

		value := entry.Value()
		if value.Kind != unstable.String {
			return invalid("", "field %q of an entry of output must be a string", key)
		}

		switch key {
		case outputPathKey:
			e.Path = string(value.Data)
		case outputToKey:
			e.To = string(value.Data)
		default:
			return invalid("", "unknown field %q of an entry of output", key)
		}
	}

	return nil
}

// OutputPaths returns the paths of the files of output, in their order.
func OutputPaths(output []OutputEntry) []string {
	paths := make([]string, 0, len(output))
	for _, entry := range output {
		paths = append(paths, entry.Path)
	}

	return paths
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
	// Output are the files the build makes, for a backend that copies them,
	// each where its entry says it goes.
	Output OutputList `toml:"output,omitempty"`
	// Incremental skips the build of a component when its inputs - the
	// build block and the files of the component, but those its .gitignore
	// files ignore - are the ones of its last build, and its outputs are
	// there; for a backend that allows it.
	Incremental *bool `toml:"incremental,omitempty"`
	// Static, of the build of any component, asks for what the build makes
	// to need nothing of the machine it is installed on, and the build checks
	// that it does. A program needs no loader and no shared library: go and
	// cargo are asked to build one so, and what cmake, make or shell built is
	// only checked. A module needs no shared
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
			if entry.To == "" {
				output = append(output, entry.Path)
			} else {
				output = append(output, map[string]any{
					outputPathKey: entry.Path, outputToKey: entry.To,
				})
			}
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

// ProgramComponent finds the component whose build makes the program named
// name, and reports its name. recorded are the programs the builds of the
// components put into .rocks/bin/, by component, as the record of the
// build of the project has them; nil when there is none. The component is,
// in this order:
//
//   - one with a build that the record says put the program there;
//   - one whose build names the program to checker (BuildCheck.Programs);
//     a nil checker knows no program;
//   - one of kind = "executable" (manifest_version 0.1, deprecated) named
//     so, whose build may make a program of any name.
func (m *Manifest) ProgramComponent(
	checker BuildChecker, name string, recorded map[string][]string,
) (string, bool) {
	names := sortedKeys(m.Components)

	for _, component := range names {
		if m.Components[component].Build != nil &&
			slices.Contains(recorded[component], name) {
			return component, true
		}
	}

	for _, component := range names {
		comp := m.Components[component]
		if comp.Build == nil || checker == nil {
			continue
		}

		check, err := checker.CheckBuild("components."+component+".build",
			m.ComponentTarget(component), *comp.Build)
		if err == nil && slices.Contains(check.Programs, name) {
			return component, true
		}
	}

	comp, ok := m.Components[name]
	if ok && comp.ProgramsOnly() {
		return name, true
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

// Hook is a lifecycle hook ([hooks.pre_build], [hooks.post_build] and
// [hooks.clean], which tt package clean of the whole project runs). It is a
// build block, but only a backend that runs hooks runs one (make and shell
// of the module build of tt), it has no component and static is refused.
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
