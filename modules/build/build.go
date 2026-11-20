// Package build is the tt module of the build backends of tt: make and
// shell, which the hooks of a package run with, c and lua-c, cargo, cmake,
// go and zig. A component whose build block names one of them is built by
// it.
//
// Each backend is a package of its own (basic, cc, cargo, cmake, gobuild,
// zig),
// written against the contract of a build backend alone,
// github.com/tarantool/tt/sdk/build/v3, as the backend of any module is. [New]
// is the constructor of the module; a distribution of tt takes it with the
// standard modules of the core.
package build

import (
	"cmp"
	"slices"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"

	"github.com/tarantool/tt/modules/build/basic"
	"github.com/tarantool/tt/modules/build/cargo"
	"github.com/tarantool/tt/modules/build/cc"
	"github.com/tarantool/tt/modules/build/cmake"
	"github.com/tarantool/tt/modules/build/gobuild"
	"github.com/tarantool/tt/modules/build/zig"
)

// New returns the module: the backends of Backends, and no command.
func New(sdk.Services) sdk.Module {
	return sdk.Module{Commands: nil, Backends: Backends(), FileChecks: nil}
}

// Backends returns the backends of the module in the order tt lists them in
// its messages: make and shell, which run the hooks, then the others by
// name.
func Backends() []sdkbuild.Backend {
	others := append(cc.Backends(), cargo.Backend(), cmake.Backend(), gobuild.Backend(),
		zig.Backend())

	slices.SortFunc(others, func(a, b sdkbuild.Backend) int { return cmp.Compare(a.Name, b.Name) })

	return append(basic.Backends(), others...)
}
