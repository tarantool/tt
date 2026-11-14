package backend

import (
	"fmt"
	"path/filepath"
	"slices"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// Spec is a build block, of a component or of a hook, as the executors of
// this package read it: the fields of every block and those of every backend
// of the core in one struct, the shape the block had before a backend had
// fields of its own. Decode makes one of a manifest.Build, whose Rest holds
// the fields of the backend.
//
// The backends of the core are registered with the SDK (Core), all of them
// with Spec for their configuration; each is to have one of its own fields
// only, and Spec is to go.
//
//nolint:recvcheck // SetBlock fills a decoded configuration; the rest read a value.
type Spec struct {
	// The fields of every build block (manifest.Build), which are no
	// fields of a backend and are not decoded from Rest.
	Backend     string            `toml:"-"`
	Cwd         string            `toml:"-"`
	Env         map[string]string `toml:"-"`
	Output      []string          `toml:"-"`
	Incremental *bool             `toml:"-"`
	Static      *bool             `toml:"-"`

	// make backend.
	MakeTarget string   `toml:"make_target"`
	Entrypoint string   `toml:"entrypoint"`
	Flags      []string `toml:"flags"`

	// shell backend.
	Command string   `toml:"command"`
	Args    []string `toml:"args"`

	// c / lua-c backends. Cflags are flags of the compiler, after tt's own
	// and before those of CFLAGS of the environment, which win a conflict.
	Module      string                  `toml:"module"`
	Sources     []string                `toml:"sources"`
	IncludeDirs []string                `toml:"include_dirs"`
	Libraries   []string                `toml:"libraries"`
	LibraryDirs []string                `toml:"library_dirs"`
	Defines     []string                `toml:"defines"`
	Cflags      []string                `toml:"cflags"`
	Platforms   map[string]BuildOverlay `toml:"platforms"`

	// go, c and lua-c backends. Ldflags are flags of the linker of the
	// backend, one per entry: of go build's -ldflags, after tt's own; of the
	// link of cc, before the sources, so that an option acts on the
	// libraries that follow them, and before LDFLAGS of the environment.
	Ldflags []string `toml:"ldflags"`

	// go and cargo backends. Package is the Go package to build (default
	// ".") or the cargo package that holds the binary.
	Package string `toml:"package"`

	// go backend.
	Name string   `toml:"name"` // Default: the component name.
	Tags []string `toml:"tags"`
	Cgo  *bool    `toml:"cgo"` // Nil or false: CGO_ENABLED=0.

	// cargo backend.
	Bin      string   `toml:"bin"` // Default: the component name.
	Features []string `toml:"features"`
	Profile  string   `toml:"profile"` // Default: release.

	// cmake backend. The build tree is out of the source, in _build/ of the
	// project, and output names files relative to it.
	Variables map[string]string `toml:"variables"`  // -D<name>=<value>.
	Targets   []string          `toml:"targets"`    // Default: all, as cmake --build.
	BuildType string            `toml:"build_type"` // Default: RelWithDebInfo.
}

// BuildOverlay is the per-OS addition to a c/lua-c build
// ([components.<name>.build.platforms.<os>]). It only adds to the base lists
// for the current OS: each list goes after the one of the build.
type BuildOverlay struct {
	IncludeDirs []string `toml:"include_dirs"`
	Libraries   []string `toml:"libraries"`
	LibraryDirs []string `toml:"library_dirs"`
	Defines     []string `toml:"defines"`
	Cflags      []string `toml:"cflags"`
	Ldflags     []string `toml:"ldflags"`
}

// DefaultCargoProfile is the cargo profile an executable is built with when
// the build names none.
const DefaultCargoProfile = "release"

// DefaultCMakeBuildType is the CMAKE_BUILD_TYPE a cmake build is configured
// with when it names none: optimized, as a release is, with the debug
// information a crash of Tarantool in the module is read with.
const DefaultCMakeBuildType = "RelWithDebInfo"

// Decode returns the Spec of the build block build: its fields, and those of
// its backend decoded from build.Rest. It is for a block Validate checked
// with the backends of the core: a field of Rest of the wrong type is an
// error, and one Spec has no place for is left out.
func Decode(build manifest.Build) (Spec, error) {
	cfg, _, err := specBackend().Decode(build.Rest)
	if err != nil {
		return Spec{}, fmt.Errorf("the build block: %w", err)
	}

	spec, ok := cfg.(*Spec)
	if !ok {
		return Spec{}, fmt.Errorf("%w: %T", errNotSpec, cfg)
	}

	spec.SetBlock(build)

	return *spec, nil
}

// specBackend is a backend whose configuration is a Spec, for its decoder.
func specBackend() sdkbuild.Backend {
	return sdkbuild.Backend{
		Name: "", Kinds: nil, Config: newSpec, New: nil, Incremental: false,
		OutputFrom: sdkbuild.OutputNone, Exclude: nil,
	}
}

// newSpec returns an empty Spec, the configuration of the backends of the
// core.
func newSpec() any {
	return new(Spec)
}

// SetBlock sets the fields of s every build block has from build. The
// registry of backends calls it before the New of a backend of the core:
// their checks read the fields of the block with those of the backend, in
// the order the block had them checked in, which the configuration of a
// backend of the SDK does not hold.
func (s *Spec) SetBlock(build manifest.Build) {
	s.Backend = build.Backend
	s.Cwd = build.Cwd
	s.Env = build.Env
	s.Output = build.Output
	s.Incremental = build.Incremental
	s.Static = build.Static
}

// IncrementalValue reports whether the build is skipped when its inputs did
// not change.
func (s Spec) IncrementalValue() bool {
	return s.Incremental != nil && *s.Incremental
}

// StaticValue reports whether the build asks for a static program or module
// (manifest.Build.Static).
func (s Spec) StaticValue() bool {
	return s.Static != nil && *s.Static
}

// CgoValue reports whether a go build may use cgo.
func (s Spec) CgoValue() bool {
	return s.Cgo != nil && *s.Cgo
}

// Executables lists the names of the programs the build of the executable
// component named component puts into .rocks/bin/, as far as the block tells
// them (manifest.Build.Executables).
func (s Spec) Executables(component string) []string {
	switch s.Backend {
	case BackendGo:
		return []string{defaultName(s.Name, component)}
	case BackendCargo:
		return []string{defaultName(s.Bin, component)}
	}

	names := make([]string, 0, len(s.Output))

	for _, output := range s.Output {
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
