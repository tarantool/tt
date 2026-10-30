package core

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/version"
)

// testConstructor is a constructor of this package defined by name.
func testConstructor(sdk.Services) []sdk.Mount { return nil }

// TestConstructorPackage checks that the package of a constructor is the
// import path the runtime names it by, for a function and a closure alike.
func TestConstructorPackage(t *testing.T) {
	t.Parallel()

	closure := func(sdk.Services) []sdk.Mount { return nil }

	assert.Equal(t, "github.com/tarantool/tt/v3/core", constructorPackage(Builtin))
	assert.Equal(t, "github.com/tarantool/tt/v3/core", constructorPackage(testConstructor))
	assert.Equal(t, "github.com/tarantool/tt/v3/core", constructorPackage(closure))
	assert.Empty(t, constructorPackage(nil))
}

// TestModuleTable checks that the table tt version --modules reads lists
// the modules by name with the packages of their constructors.
func TestModuleTable(t *testing.T) {
	t.Parallel()

	got := moduleTable(Modules{"zeta": testConstructor, "builtin": Builtin, "none": nil})

	assert.Equal(t, []version.Module{
		{Name: "builtin", Package: "github.com/tarantool/tt/v3/core"},
		{Name: "none", Package: ""},
		{Name: "zeta", Package: "github.com/tarantool/tt/v3/core"},
	}, got)
}
