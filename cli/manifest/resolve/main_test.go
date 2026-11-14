package resolve_test

import (
	"os"
	"testing"

	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// TestMain makes the backends of the tests (buildregtest) those of the
// process, as the core makes those of its modules: the resolver checks the
// manifests of path dependencies with them.
func TestMain(m *testing.M) {
	buildreg.SetProcess(buildregtest.Registry())
	os.Exit(m.Run())
}
