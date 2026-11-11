package install

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
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

// TestInstallDepsPinsArtifacts covers a refetch of a rock whose lock pins its
// files: the pins reach the installer, a decision without them installs with
// nil pins, and a refusal for them says what to do.
func TestInstallDepsPinsArtifacts(t *testing.T) {
	t.Parallel()

	pins := map[string]string{"linux-x86_64": strings.Repeat("a", 64)}
	fake := &fakeInstaller{calls: nil, names: nil, err: nil}

	decisions := []depDecision{
		{name: "luasocket", version: "3.0.4", source: fromRegistry, removeVersion: ""},
		{
			name: "emmylua", version: "0.30.0pre1-1", source: fromRegistry, removeVersion: "",
			artifacts: pins,
		},
	}

	require.NoError(t, installDeps(context.Background(), fake, decisions, nil, nil))
	assert.Nil(t, fake.calls[0].Artifacts)
	assert.Equal(t, pins, fake.calls[1].Artifacts)

	fake.err = fmt.Errorf("rocks.Install: %w", luarocks.ErrArtifactNotPinned)

	err := installDeps(context.Background(), fake, decisions[1:], nil, nil)
	require.ErrorIs(t, err, luarocks.ErrArtifactNotPinned)
	assert.Contains(t, err.Error(), "pack the package again after tt package resolve")
}

// TestPlanDepsCarriesArtifactPins checks that a registry decision takes the
// pins of the lock entry its version comes from: the archive's, for the
// version it carries.
func TestPlanDepsCarriesArtifactPins(t *testing.T) {
	t.Parallel()

	sum := strings.Repeat("b", 64)
	header := &Header{
		Manifest: &manifest.Manifest{},
		Lock: &manifest.Lock{Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{{
				Name: "emmylua", Version: "0.30.0pre1-1", Source: sourceRegistry,
				Artifacts: map[string]string{"macosx-aarch64": "sha256:" + sum},
			}}},
		}},
	}

	plan, err := planDeps(header, "default", nil, nil, false)
	require.NoError(t, err)
	require.Len(t, plan.decisions, 1)
	assert.Equal(t, fromRegistry, plan.decisions[0].source)
	assert.Equal(t, map[string]string{"macosx-aarch64": sum}, plan.decisions[0].artifacts)
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
