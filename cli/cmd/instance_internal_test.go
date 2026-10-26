package cmd

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/running"
)

// instanceSubcommands are the commands of tt instance.
var instanceSubcommands = []string{
	"check", "clean", "connect", "kill", "logrotate", "logs", "restart", "start", "status",
	"stop",
}

// TestInstanceGroup pins the commands of tt instance, and that the old
// top-level commands are gone, without shims: tt connect stays, as the
// facade of tt instance connect.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestInstanceGroup(t *testing.T) {
	group := NewInstanceCmd()

	names := make([]string, 0, len(group.Commands()))
	for _, sub := range group.Commands() {
		names = append(names, sub.Name())
	}

	assert.Equal(t, instanceSubcommands, names)

	top := make([]string, 0, len(BuiltinCommands()))
	for _, command := range BuiltinCommands() {
		top = append(top, command.Name())
	}

	assert.Contains(t, top, "instance")
	assert.Contains(t, top, "connect")

	for _, gone := range []string{
		"start", "stop", "restart", "kill", "status", "log", "logs", "logrotate", "check",
		"clean",
	} {
		assert.NotContains(t, top, gone)
	}
}

// TestInstanceGroupFlags pins the flags the commands of tt instance share,
// and that -f is no short form of anything but --force: not --follow, not
// --format, not --force of a confirmation. tt instance connect keeps -f of
// its script file for now.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestInstanceGroupFlags(t *testing.T) {
	for _, sub := range NewInstanceCmd().Commands() {
		t.Run(sub.Name(), func(t *testing.T) {
			for _, name := range []string{"scope", "template"} {
				flag := sub.Flags().Lookup(name)
				require.NotNil(t, flag, name)
				assert.Empty(t, flag.Shorthand, name)
				assert.False(t, flag.Hidden, name)
			}

			if sub.Name() == "connect" {
				return
			}

			assert.Nil(t, sub.Flags().ShorthandLookup("f"))
		})
	}

	group := NewInstanceCmd()

	find := func(name string) *cobra.Command {
		sub, _, err := group.Find([]string{name})
		require.NoError(t, err)

		return sub
	}

	start := find("start")
	foreground := start.Flags().Lookup("foreground")
	require.NotNil(t, foreground)
	assert.Empty(t, foreground.Shorthand)
	assert.Nil(t, start.Flags().Lookup("interactive"))

	for _, hidden := range []string{"watchdog", "app-dir"} {
		flag := start.Flags().Lookup(hidden)
		require.NotNil(t, flag, hidden)
		assert.True(t, flag.Hidden, hidden)
	}

	follow := find("logs").Flags().Lookup("follow")
	require.NotNil(t, follow)
	assert.Empty(t, follow.Shorthand)

	assert.NotNil(t, find("status").Flags().ShorthandLookup("o"))

	for _, name := range []string{"stop", "restart", "kill", "clean"} {
		yes := find(name).Flags().Lookup("yes")
		require.NotNil(t, yes, name)
		assert.Equal(t, "y", yes.Shorthand, name)
		assert.Nil(t, find(name).Flags().Lookup("force"), name)
	}
}

// TestConnectFacade pins that tt connect is tt instance connect: the same
// implementation with the same flags.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestConnectFacade(t *testing.T) {
	facade := NewConnectCmd()

	instance, _, err := NewInstanceCmd().Find([]string{"connect"})
	require.NoError(t, err)

	assert.Equal(t, instance.Flags().FlagUsages(), facade.Flags().FlagUsages())
	assert.Equal(t, instance.Name(), facade.Name())
	assert.Contains(t, instance.Use, "tt instance connect")
	assert.Contains(t, facade.Use, "tt connect")
}

// TestStartOptionsRefuse pins the flags of tt instance start that do not go
// together.
//
//nolint:paralleltest // Sets the flags of tt instance start.
func TestStartOptionsRefuse(t *testing.T) {
	previousWatchdog, previousDir, previousForeground := watchdog, watchdogDir, startForeground
	previousFlags := instanceFlags

	t.Cleanup(func() {
		watchdog, watchdogDir, startForeground = previousWatchdog, previousDir, previousForeground
		instanceFlags = previousFlags
	})

	cmdCtx := &cmdcontext.CmdCtx{}

	watchdog, watchdogDir, startForeground = false, "/somewhere", false

	_, err := startOptions(cmdCtx)
	require.ErrorContains(t, err, "--app-dir is only for the watchdog")

	watchdog, watchdogDir, startForeground = false, "", false
	instanceFlags.Scope = "global"

	_, err = startOptions(cmdCtx)
	require.ErrorContains(t, err, `unknown scope "global"`)
}

// errBroken is the failure of an instance in the tests of instancesResult.
var errBroken = errors.New("broken")

// TestInstancesResult pins the outcome of an instance command that acts on
// several instances: 0 when all went through, 3 when some did, 1 when none
// did, and the error of the instance itself when there is one instance.
func TestInstancesResult(t *testing.T) {
	system := sdk.WithCode(sdk.ExitSystem, errBroken)

	cases := []struct {
		name   string
		total  int
		failed map[string]error
		code   int
		same   error
	}{
		{name: "every instance", total: 3, failed: nil, code: sdk.ExitOK},
		{
			name: "some instances", total: 3, failed: map[string]error{"app:b": errBroken},
			code: sdk.ExitPartial,
		},
		{
			name: "every instance failed", total: 2,
			failed: map[string]error{"app:a": system, "app:b": errBroken},
			code:   sdk.ExitFailure,
		},
		{
			name: "one instance keeps its error", total: 1,
			failed: map[string]error{"app:a": system}, code: sdk.ExitSystem, same: system,
		},
		{
			name: "one silent instance", total: 1,
			failed: map[string]error{"app:a": exitcode.Silent(errBroken)},
			code:   sdk.ExitFailure,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := newInstancesResult("stop", test.total)

			for _, name := range []string{"app:a", "app:b", "app:c"}[:test.total] {
				if err, failed := test.failed[name]; failed {
					result.fail(name, err)
				}
			}

			err := result.err()
			assert.Equal(t, test.code, exitcode.Code(err))

			if test.same != nil {
				assert.Same(t, test.same, err)
			}

			if test.code == sdk.ExitPartial || test.total > 1 && test.code == sdk.ExitFailure {
				require.ErrorIs(t, err, errInstancesFailed)
				assert.ErrorContains(t, err, "stop failed for")
			}
		})
	}
}

// TestInstancesResultFailAll pins that a failure before the instances are
// acted on fails every one of them.
func TestInstancesResultFailAll(t *testing.T) {
	result := newInstancesResult("restart", 3)

	result.fail("app:a", errBroken)
	result.failAll([]string{"app:b", "app:c"}, errBroken)

	err := result.err()
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	require.ErrorContains(t, err, "restart failed for 3 of 3 instances: app:a, app:b, app:c")

	result = newInstancesResult("restart", 3)
	result.failAll(nil, errBroken)
	require.NoError(t, result.err())
}

// TestActOnInstancesGoesOn pins that a command goes on after an instance it
// fails to act on.
func TestActOnInstancesGoesOn(t *testing.T) {
	instances := []running.InstanceCtx{
		{AppName: "app", InstName: "a"},
		{AppName: "app", InstName: "b"},
		{AppName: "app", InstName: "c"},
	}

	var acted []string

	err := actOnInstances("kill", instances, func(inst *running.InstanceCtx) error {
		acted = append(acted, inst.InstName)

		if inst.InstName == "a" {
			return errBroken
		}

		return nil
	})

	assert.Equal(t, []string{"a", "b", "c"}, acted)
	assert.Equal(t, sdk.ExitPartial, exitcode.Code(err))
	assert.ErrorContains(t, err, "kill failed for 1 of 3 instances: app:a")
}

// TestCompleteInstanceTargets pins the completion of the target of an
// instance command: the project and the applications of the system scope,
// each with the colon, then the instances of the pair the filter keeps, in
// the item of a list being typed.
//
//nolint:paralleltest // Changes the working directory.
func TestCompleteInstanceTargets(t *testing.T) {
	externalProject(t)

	dir, err := absoluteWorkingDir()
	require.NoError(t, err)

	t.Chdir(dir)

	cmd := NewInstanceCmd()

	stop, _, err := cmd.Find([]string{"stop"})
	require.NoError(t, err)

	cmdCtx := &cmdcontext.CmdCtx{}

	got, directive := internal.ValidArgsFunction(cmdCtx, stop, "", internal.Any)
	assert.Equal(t, []string{"myapp:"}, got)
	assert.NotZero(t, directive&cobra.ShellCompDirectiveNoSpace)

	got, _ = internal.ValidArgsFunction(cmdCtx, stop, "myapp:", internal.Any)
	assert.Equal(t, []string{"myapp:storage-1", "myapp:worker-1", "myapp:storage-2"}, got)

	got, _ = internal.ValidArgsFunction(cmdCtx, stop, "myapp:storage-1,myapp:w", internal.Any)
	assert.Equal(t, []string{
		"myapp:storage-1,myapp:storage-1", "myapp:storage-1,myapp:worker-1",
		"myapp:storage-1,myapp:storage-2",
	}, got)

	got, _ = internal.ValidArgsFunction(cmdCtx, stop, "myapp:", internal.Active)
	assert.Empty(t, got, "nothing runs")

	got, _ = internal.ValidArgsFunction(cmdCtx, stop, "other:", internal.Any)
	assert.Empty(t, got)

	require.NoError(t, stop.Flags().Set("scope", "system"))
	t.Cleanup(func() { instanceFlags.Scope = "" })

	got, _ = internal.ValidArgsFunction(cmdCtx, stop, "", internal.Any)
	assert.Empty(t, got, "the system scope holds nothing")
}
