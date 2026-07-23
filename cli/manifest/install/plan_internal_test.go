package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// headerFor parses an archiveSpec into a Header without writing an archive, for
// plan-level tests.
func headerFor(t *testing.T, spec archiveSpec) *Header {
	t.Helper()

	ar, err := OpenArchive(spec.build(t))
	require.NoError(t, err)

	header, err := ar.ReadHeader()
	require.NoError(t, err)

	return header
}

// TestPlanWithoutDepsRoutesToRegistry covers the --without-deps routing: a
// dependency the archive does not carry and the tree does not have is fetched
// from the registry.
func TestPlanWithoutDepsRoutesToRegistry(t *testing.T) {
	t.Parallel()

	header := headerFor(t, archiveSpec{
		name: "some-lib", version: "2.0.0",
		deps:     map[string]string{"luasocket": ">=3.0.0"},
		lockDeps: []LockDep{{Name: "luasocket", Version: "3.0.4", Source: "registry"}},
	})

	lay, err := resolveLayout(ScopeProject, t.TempDir())
	require.NoError(t, err)

	plan, err := planDeps(header, "default", nil, lay, false)
	require.NoError(t, err)

	require.Len(t, plan.decisions, 1)
	assert.Equal(t, "luasocket", plan.decisions[0].name)
	assert.Equal(t, "3.0.4", plan.decisions[0].version)
	assert.Equal(t, fromRegistry, plan.decisions[0].source)
}

// TestPlanWithDepsRoutesToArchive covers the with-deps routing: the same
// dependency is taken from the archive, not the registry.
func TestPlanWithDepsRoutesToArchive(t *testing.T) {
	t.Parallel()

	header := headerFor(t, archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		deps:     map[string]string{"luasocket": ">=3.0.0"},
		lockDeps: []LockDep{{Name: "luasocket", Version: "3.0.4", Source: "registry"}},
	})

	lay, err := resolveLayout(ScopeProject, t.TempDir())
	require.NoError(t, err)

	plan, err := planDeps(header, "default", nil, lay, true)
	require.NoError(t, err)

	require.Len(t, plan.decisions, 1)
	assert.Equal(t, fromArchive, plan.decisions[0].source)
}
