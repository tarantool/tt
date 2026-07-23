package install

import (
	"context"
	"errors"
	"testing"

	"github.com/tarantool/go-luarocks/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInstaller records the Install calls the refetch loop makes, standing in
// for a registry so the --without-deps download path is exercised offline.
type fakeInstaller struct {
	calls []client.InstallOpts
	names []string
	err   error
}

func (f *fakeInstaller) Install(_ context.Context, name string, opts client.InstallOpts) error {
	f.names = append(f.names, name)
	f.calls = append(f.calls, opts)

	return f.err
}

// TestInstallDepsRefetchesPins covers the --without-deps download loop: each
// registry decision is installed at its exact pin with dependency resolution
// off, so nothing re-resolves its own closure.
func TestInstallDepsRefetchesPins(t *testing.T) {
	t.Parallel()

	fake := &fakeInstaller{calls: nil, names: nil, err: nil}

	decisions := []depDecision{
		{name: "luasocket", version: "3.0.4", source: fromRegistry, removeVersion: ""},
		{name: "inspect", version: "3.1.3", source: fromRegistry, removeVersion: ""},
	}

	err := installDeps(context.Background(), fake, decisions, []string{"srv"}, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"luasocket", "inspect"}, fake.names)

	for _, opts := range fake.calls {
		assert.Equal(t, client.DepsNone, opts.Deps, "the closure is complete; no re-resolution")
		assert.Equal(t, []string{"srv"}, opts.Servers)
	}

	assert.Equal(t, "3.0.4", fake.calls[0].Version)
	assert.Equal(t, "3.1.3", fake.calls[1].Version)
}

// TestInstallDepsPropagatesError covers a registry failure surfacing with the
// dependency named.
func TestInstallDepsPropagatesError(t *testing.T) {
	t.Parallel()

	fake := &fakeInstaller{calls: nil, names: nil, err: errors.New("registry down")}

	decisions := []depDecision{
		{name: "luasocket", version: "3.0.4", source: fromRegistry, removeVersion: ""},
	}

	err := installDeps(context.Background(), fake, decisions, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "luasocket 3.0.4")
}

// TestRegistryDecisionsSelectsOnlyRegistry checks that only registry-sourced
// decisions are refetched — archived and skipped ones never hit the network.
func TestRegistryDecisionsSelectsOnlyRegistry(t *testing.T) {
	t.Parallel()

	plan := installPlan{
		decisions: []depDecision{
			{name: "a", version: "1", source: fromArchive, removeVersion: ""},
			{name: "b", version: "2", source: fromRegistry, removeVersion: ""},
			{name: "c", version: "3", source: skipDep, removeVersion: ""},
		},
		skipNames: nil,
	}

	got := registryDecisions(plan)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].name)
}
