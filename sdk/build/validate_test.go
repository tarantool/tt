package build_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// plainBuild is a Build with no optional interface.
type plainBuild struct{}

func (plainBuild) Run(context.Context, *build.Context) error { return nil }

// fullBuild is a Build of every optional interface.
type fullBuild struct{ plainBuild }

var (
	_ build.Preflighter  = fullBuild{}
	_ build.Producer     = fullBuild{}
	_ build.ProgramNamer = fullBuild{}
	_ build.InputHasher  = fullBuild{}
	_ build.Cleaner      = fullBuild{}
	_ build.Vendorer     = fullBuild{}
)

func (fullBuild) Preflight(context.Context, *build.Context) error          { return nil }
func (fullBuild) Produces() []build.Result                                 { return nil }
func (fullBuild) Programs() ([]string, bool)                               { return nil, false }
func (fullBuild) Inputs(context.Context, *build.Context) ([]string, error) { return nil, nil }
func (fullBuild) Clean(context.Context, *build.Context) error              { return nil }
func (fullBuild) Vendor(context.Context, *build.Context, string) error     { return nil }

// cleanWithoutContext is a Build whose Clean takes no context.
type cleanWithoutContext struct{ plainBuild }

func (cleanWithoutContext) Clean(*build.Context) error { return nil }

// vendorWithoutDir is a Build whose Vendor is told no directory.
type vendorWithoutDir struct{ plainBuild }

func (vendorWithoutDir) Vendor(context.Context, *build.Context) error { return nil }

// oldPreflight is a Build whose Preflight is that of an older SDK.
type oldPreflight struct{ plainBuild }

func (oldPreflight) Preflight(*build.Context) error { return nil }

// oldInputs is a Build whose Inputs is that of an older SDK.
type oldInputs struct{ plainBuild }

func (oldInputs) Inputs(*build.Context) ([]string, error) { return nil, nil }

// pointerPreflight is a Build whose Preflight has a pointer receiver.
type pointerPreflight struct{ plainBuild }

func (*pointerPreflight) Preflight(context.Context, *build.Context) error { return nil }

// wrongPrograms is a Build whose Programs returns too little.
type wrongPrograms struct{ plainBuild }

func (wrongPrograms) Programs() []string { return nil }

// exportBuild is a Build made without tt too.
type exportBuild struct{ plainBuild }

var _ build.Exporter = exportBuild{}

func (exportBuild) Export(*build.ExportContext) (build.Export, error) {
	return build.Export{Script: "true", Tools: nil, Lost: nil}, nil
}

// exportWithoutContext is a Build whose Export is told nothing.
type exportWithoutContext struct{ plainBuild }

func (exportWithoutContext) Export() (build.Export, error) {
	return build.Export{Script: "true", Tools: nil, Lost: nil}, nil
}

// pointerExport is a Build whose Export, of another signature, has a pointer
// receiver.
type pointerExport struct{ plainBuild }

func (*pointerExport) Export(string) error { return nil }

// TestValidateBuildExports checks that a Build is an Exporter when its
// backend says its builds are made without tt, and only then, that an Export
// of another signature is refused when the backend says so, and that one of
// a backend that does not, a method of its own, is let through.
func TestValidateBuildExports(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // Only the name and Exports are told.
	exports, plain := build.Backend{Name: "demo", Exports: true}, build.Backend{Name: "demo"}

	require.NoError(t, exports.ValidateBuild(exportBuild{}))
	require.NoError(t, exports.ValidateBuild(&exportBuild{}))
	require.NoError(t, plain.ValidateBuild(plainBuild{}))

	// A backend written before Exports may have a method Export of its own.
	require.NoError(t, plain.ValidateBuild(exportWithoutContext{}))
	require.NoError(t, plain.ValidateBuild(&exportWithoutContext{}))
	require.NoError(t, plain.ValidateBuild(pointerExport{}))

	for _, test := range []struct {
		name    string
		backend build.Backend
		bld     build.Build
		msg     string
	}{
		{"no Export of a backend that exports", exports, plainBuild{}, `invalid build ` +
			`backend "demo": the backend says its builds are made without tt (Exports), and ` +
			`its build build_test.plainBuild has no Export of build.Exporter`},
		{"an Export of a backend that does not export", plain, exportBuild{}, `invalid ` +
			`build backend "demo": its build build_test.exportBuild has Export of ` +
			`build.Exporter, and the backend does not say its builds are made without tt: ` +
			`set Exports`},
		{"an Export of another signature", exports, exportWithoutContext{}, `Export of its ` +
			`build build_test.exportWithoutContext is func() (build.Export, error), not ` +
			`func(*build.ExportContext) (build.Export, error) of build.Exporter`},
		{"an Export of a pointer of another signature", exports, pointerExport{}, `Export ` +
			`of its build is a method of *build_test.pointerExport, but New returns a ` +
			`build_test.pointerExport: the core would never call it`},
		{"no Export of a backend that exports, but one of its own", exports,
			&exportWithoutContext{}, `Export of its build *build_test.exportWithoutContext ` +
				`is func() (build.Export, error)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.backend.ValidateBuild(test.bld)
			require.ErrorIs(t, err, build.ErrBackend)
			assert.Contains(t, err.Error(), test.msg)
		})
	}
}

// TestValidateBuild checks that a method of a Build named as the method of
// an optional interface of another signature is refused, since the core
// would never call it.
func TestValidateBuild(t *testing.T) {
	t.Parallel()

	backend := build.Backend{Name: "demo"} //nolint:exhaustruct // Only the name is told.

	require.NoError(t, backend.ValidateBuild(plainBuild{}))
	require.NoError(t, backend.ValidateBuild(fullBuild{}))
	require.NoError(t, backend.ValidateBuild(&fullBuild{}))
	require.NoError(t, backend.ValidateBuild(&pointerPreflight{}))

	for _, test := range []struct {
		name string
		bld  build.Build
		msg  string
	}{
		{"a Preflight without a context", oldPreflight{}, `invalid build backend "demo": ` +
			`Preflight of its build build_test.oldPreflight is func(*build.Context) error, ` +
			`not func(context.Context, *build.Context) error of build.Preflighter: ` +
			`the core would never call it`},
		{"an Inputs without a context", oldInputs{}, `Inputs of its build ` +
			`build_test.oldInputs is func(*build.Context) ([]string, error), not ` +
			`func(context.Context, *build.Context) ([]string, error) of build.InputHasher`},
		{"a Preflight of a pointer", pointerPreflight{}, `invalid build backend "demo": ` +
			`Preflight of its build is a method of *build_test.pointerPreflight, but New ` +
			`returns a build_test.pointerPreflight: the core would never call it`},
		{"a Programs of another signature", wrongPrograms{}, `Programs of its build ` +
			`build_test.wrongPrograms is func() []string, not func() ([]string, bool) of ` +
			`build.ProgramNamer`},
		{"a Clean without a context", cleanWithoutContext{}, `Clean of its build ` +
			`build_test.cleanWithoutContext is func(*build.Context) error, not ` +
			`func(context.Context, *build.Context) error of build.Cleaner`},
		{"a Vendor without a directory", vendorWithoutDir{}, `Vendor of its build ` +
			`build_test.vendorWithoutDir is func(context.Context, *build.Context) error, not ` +
			`func(context.Context, *build.Context, string) error of build.Vendorer`},
		{"no build", nil, `invalid build backend "demo": New returned a nil Build`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := backend.ValidateBuild(test.bld)
			require.ErrorIs(t, err, build.ErrBackend)
			assert.Contains(t, err.Error(), test.msg)
		})
	}
}
