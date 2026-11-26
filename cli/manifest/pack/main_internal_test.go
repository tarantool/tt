package pack

import (
	"os"
	"testing"

	"github.com/tarantool/tt/modules/build/cc"

	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// TestMain makes the backends of the tests (buildregtest) those of the
// process, as the core makes those of its modules: a pack builds the
// packages of the tests with them. c and lua-c are those of tt, of the
// module build, whose builds LuaRocks makes by the scripts of their exports
// (rockscript_cc_internal_test.go).
func TestMain(m *testing.M) {
	registry := buildregtest.Registry()

	err := registry.Add("build", cc.Backends(), nil)
	if err != nil {
		panic(err)
	}

	buildreg.SetProcess(registry)
	os.Exit(m.Run())
}
