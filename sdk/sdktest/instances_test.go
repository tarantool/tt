package sdktest_test

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// localInstance is an instance of the fake of the name, with a crash.
func localInstance(name string) sdk.LocalInstance {
	return sdk.LocalInstance{
		Name: name, WorkDir: "/app", RunDir: "/app/var/run/" + name, Log: "/app/tt.log",
		TarantoolLog: "",
		Crashes: []sdk.Crash{{
			At: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), PID: 42,
			Signal: syscall.SIGSEGV, CoreDumped: true,
		}},
	}
}

// names returns the names of instances, in order.
func names(instances []sdk.LocalInstance) []string {
	found := make([]string, 0, len(instances))
	for _, inst := range instances {
		found = append(found, inst.Name)
	}

	return found
}

// TestServicesLocalInstances checks the selection of the fake, as the core
// selects the instances of this host: none is all, <app>:* those of the
// application, <app>:<instance> one, a comma-separated list each of them
// once, in the order of the fake, and the calls are recorded with their
// lookup.
func TestServicesLocalInstances(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithLocalInstances(
		localInstance("app:a"), localInstance("app:b"), localInstance("other:c")))
	services.Start()

	all, err := services.LocalInstances(t.Context(), nil, sdk.AppLookup{})
	require.NoError(t, err)
	assert.Equal(t, []string{"app:a", "app:b", "other:c"}, names(all))
	assert.Equal(t, localInstance("app:a"), all[0])

	cases := map[string][]string{
		"app:*":       {"app:a", "app:b"},
		"other:c":     {"other:c"},
		"app:b,app:*": {"app:a", "app:b"},
	}

	for target, want := range cases {
		got, err := services.LocalInstances(t.Context(), []string{target},
			sdk.AppLookup{Scope: "user"})
		require.NoError(t, err, target)
		assert.Equal(t, want, names(got), target)
	}

	// The applications come in the order the targets name them, and the
	// instances of each in its order, as the core lists them.
	orders := map[string][][]string{
		"other, app": {{"other:c", "app:a"}, {"other:c", "app:a"}},
		"app, other": {{"app:b", "other:c", "app:a"}, {"app:a", "app:b", "other:c"}},
		"list":       {{"other:*,app:b"}, {"other:c", "app:b"}},
	}

	for name, order := range orders {
		got, err := services.LocalInstances(t.Context(), order[0], sdk.AppLookup{})
		require.NoError(t, err, name)
		assert.Equal(t, order[1], names(got), name)
	}

	for _, target := range []string{"app:x", "none:*"} {
		_, err = services.LocalInstances(t.Context(), []string{target}, sdk.AppLookup{})
		require.ErrorIs(t, err, sdk.ErrNotFound, target)
	}

	// The targets that are none the core refuses with these messages, which
	// its test pins; an instance target before them selects nothing.
	refused := map[string]string{
		"app": `invalid target "app": an instance-level command needs an instance target; ` +
			`use app:<INSTANCE> or app:*`,
		"app:": `invalid target "app:": the instance name is missing; use app:<INSTANCE> ` +
			`or app:*`,
		":a":      `invalid target ":a": the application name is missing; use <APP>:a`,
		"app:a,":  `invalid target: empty target in a list`,
		"app:x,:": `invalid target ":": the application name is missing; use <APP>:`,
	}

	for target, message := range refused {
		_, err = services.LocalInstances(t.Context(), []string{target}, sdk.AppLookup{})
		require.EqualError(t, err, message, target)
		require.NotErrorIs(t, err, sdk.ErrNotFound, target)
	}

	// What a module changes of a result is its own.
	all[0].Crashes[0].PID = 7

	again, err := services.LocalInstances(t.Context(), []string{"app:a"}, sdk.AppLookup{})
	require.NoError(t, err)
	assert.Equal(t, 42, again[0].Crashes[0].PID)

	calls := services.LocalInstancesCalls()
	require.Len(t, calls, 15)
	assert.Equal(t, sdktest.LocalInstancesCall{Targets: nil, Lookup: sdk.AppLookup{}}, calls[0])
	assert.Equal(t, sdk.AppLookup{Scope: "user"}, calls[1].Lookup)
}

// TestServicesLocalInstancesCrashesErr checks that an instance whose crashes
// cannot be read comes as it is given, with why, as the core gives it.
func TestServicesLocalInstancesCrashesErr(t *testing.T) {
	t.Parallel()

	unreadable := errors.New("cannot read the crashes: permission denied")
	services := sdktest.New(t, sdktest.WithLocalInstances(
		sdk.LocalInstance{Name: "app:a", CrashesErr: unreadable}, localInstance("app:b")))
	services.Start()

	got, err := services.LocalInstances(t.Context(), []string{"app:*"}, sdk.AppLookup{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.ErrorIs(t, got[0].CrashesErr, unreadable)
	assert.Empty(t, got[0].Crashes)
	require.NoError(t, got[1].CrashesErr)
}

// TestServicesLocalInstancesNone checks the fake without instances and with
// a context that is done.
func TestServicesLocalInstancesNone(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	all, err := services.LocalInstances(t.Context(), nil, sdk.AppLookup{})
	require.NoError(t, err)
	assert.Empty(t, all)

	_, err = services.LocalInstances(t.Context(), []string{"app:a"}, sdk.AppLookup{})
	require.ErrorIs(t, err, sdk.ErrNotFound)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = services.LocalInstances(ctx, nil, sdk.AppLookup{})
	require.ErrorIs(t, err, context.Canceled)
}
