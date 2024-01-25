//go:build integration

package replicaset_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/go-tarantool/test_helpers"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

const workDir = "work_dir"
const server = "127.0.0.1:3013"
const console = workDir + "/" + "console.control"

var opts = tarantool.Opts{
	Timeout: 500 * time.Millisecond,
	User:    "test",
	Pass:    "password",
}

func doRequest(req tarantool.Request) error {
	conn, err := tarantool.Connect(server, opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Do(req).Get()
	return err
}

func setTarantoolVersion(str string) error {
	req := tarantool.NewCallRequest("set_tarantool_version").Args([]any{str})
	return doRequest(req)
}

func resetTarantoolVersion() error {
	req := tarantool.NewCallRequest("reset_tarantool_version")
	return doRequest(req)
}

func testConnect(t *testing.T) connector.Connector {
	t.Helper()

	conn, err := connector.Connect(connector.ConnectOpts{
		Network: "unix",
		Address: console,
	})
	require.NoError(t, err)

	return conn
}

func setCartridgeTestEnvironment(t *testing.T, version string) func() {
	t.Helper()
	require.NoError(t, setTarantoolVersion(version))

	eval := tarantool.NewEvalRequest(`package.loaded["cartridge"] = {}`)
	require.NoError(t, doRequest(eval))

	return func() {
		require.NoError(t, resetTarantoolVersion())

		eval := tarantool.NewEvalRequest(`package.loaded["cartridge"] = nil`)
		require.NoError(t, doRequest(eval))
	}
}

func setCConfigTestEnvironment(t *testing.T) func() {
	t.Helper()
	require.NoError(t, setTarantoolVersion("3.0.0-foo-bar"))

	return func() {
		require.NoError(t, resetTarantoolVersion())
	}
}

func setCustomTestEnvironment(t *testing.T, version string) func() {
	t.Helper()
	err := setTarantoolVersion(version)
	require.NoError(t, err)

	return func() {
		require.NoError(t, resetTarantoolVersion())
	}
}

func TestEvalOrchestrator_cartridge(t *testing.T) {
	for _, tc := range []string{"1.10.11-foo-bar", "2-0-0-foo-bar", "2.2.2-foo-bar"} {
		t.Run(tc, func(t *testing.T) {
			reset := setCartridgeTestEnvironment(t, tc)
			defer reset()

			conn := testConnect(t)
			defer conn.Close()

			evaled, err := replicaset.EvalOrchestrator(conn)
			require.NoError(t, err)
			require.Equal(t, replicaset.OrchestratorCartridge, evaled)
		})
	}
}

func TestEvalOrchestrator_cconfig(t *testing.T) {
	reset := setCConfigTestEnvironment(t)
	defer reset()

	conn := testConnect(t)
	defer conn.Close()

	evaled, err := replicaset.EvalOrchestrator(conn)
	require.NoError(t, err)
	require.Equal(t, replicaset.OrchestratorCentralizedConfig, evaled)
}

func TestEvalOrchestrator_cconfig_if_cartridge(t *testing.T) {
	reset := setCartridgeTestEnvironment(t, "3.0.0-foo-bar")
	defer reset()

	conn := testConnect(t)
	defer conn.Close()

	evaled, err := replicaset.EvalOrchestrator(conn)
	require.NoError(t, err)
	require.Equal(t, replicaset.OrchestratorCentralizedConfig, evaled)
}

func TestEvalOrchestrator_custom(t *testing.T) {
	for _, tc := range []string{"1.10.11-foo-bar", "2-0-0-foo-bar", "2.2.2-foo-bar"} {
		t.Run(tc, func(t *testing.T) {
			reset := setCustomTestEnvironment(t, tc)
			defer reset()

			conn := testConnect(t)
			defer conn.Close()

			evaled, err := replicaset.EvalOrchestrator(conn)
			require.NoError(t, err)
			require.Equal(t, replicaset.OrchestratorCustom, evaled)
		})
	}
}

type instanceEvalerMock struct {
	T         *testing.T
	Instances []running.InstanceCtx
	Done      bool
	Error     error
}

func (e *instanceEvalerMock) Eval(instance running.InstanceCtx,
	evaler connector.Evaler) (bool, error) {
	e.Instances = append(e.Instances, instance)

	// Ensure that we already connected to the instance.
	require.NotNil(e.T, evaler)
	data, err := evaler.Eval("return box.cfg.listen", []any{}, connector.RequestOpts{})
	require.NoError(e.T, err)
	require.Equal(e.T, []any{"127.0.0.1:3013"}, data)

	return e.Done, e.Error
}

func TestEvalForeach(t *testing.T) {
	connectable := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}

	cases := []struct {
		Name      string
		Instances []running.InstanceCtx
	}{
		{
			"single",
			[]running.InstanceCtx{
				connectable,
			},
		},
		{
			"multiple",
			[]running.InstanceCtx{
				connectable,
				running.InstanceCtx{
					AppName:       "bar",
					ConsoleSocket: console,
				},
				running.InstanceCtx{
					AppName:       "bar2",
					ConsoleSocket: console,
				},
				connectable,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &instanceEvalerMock{T: t}
			err := replicaset.EvalForeach(tc.Instances, evaler)
			assert.NoError(t, err)
			assert.Equal(t, tc.Instances, evaler.Instances)
		})
	}
}

func TestEvalForeachAlive_stops_after_failed_to_connect(t *testing.T) {
	validInstance := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}
	instances := []running.InstanceCtx{
		validInstance,
		running.InstanceCtx{
			AppName:       "app",
			InstName:      "instance",
			ConsoleSocket: "unreachetable",
		},
		validInstance,
	}

	evaler := &instanceEvalerMock{T: t}
	err := replicaset.EvalForeach(instances, evaler)
	assert.ErrorContains(t, err, "failed to connect to 'app:instance'")
	assert.Equal(t, []running.InstanceCtx{validInstance}, evaler.Instances)
}

func TestEvalForeach_stops_after_evaler_done(t *testing.T) {
	validInstance := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}
	instances := []running.InstanceCtx{
		validInstance,
		validInstance,
	}

	evaler := &instanceEvalerMock{T: t, Done: true}
	err := replicaset.EvalForeach(instances, evaler)
	assert.NoError(t, err)
	assert.Equal(t, evaler.Instances, []running.InstanceCtx{validInstance})
}

func TestEvalForeach_stops_after_evaler_error(t *testing.T) {
	validInstance := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}
	instances := []running.InstanceCtx{
		validInstance,
		validInstance,
	}

	evaler := &instanceEvalerMock{T: t, Error: fmt.Errorf("foo")}
	err := replicaset.EvalForeach(instances, evaler)
	assert.EqualError(t, err, "foo")
	assert.Equal(t, evaler.Instances, []running.InstanceCtx{validInstance})
}

func TestEvalForeach_error(t *testing.T) {
	type unimplementedEvaler struct {
		replicaset.InstanceEvaler
	}

	cases := []struct {
		Name      string
		Instances []running.InstanceCtx
		Expected  string
	}{
		{"no_instances", []running.InstanceCtx{}, "no instances to connect"},
		{
			"no_connection",
			[]running.InstanceCtx{
				running.InstanceCtx{
					AppName:       "app",
					InstName:      "inst",
					ConsoleSocket: "unreachetable",
				},
			},
			"failed to connect to 'app:inst'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			err := replicaset.EvalForeach(tc.Instances, unimplementedEvaler{})
			require.ErrorContains(t, err, tc.Expected)
		})
	}
}

func TestEvalForeachAlive(t *testing.T) {
	connectable := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}

	cases := []struct {
		Name      string
		Instances []running.InstanceCtx
		Expected  []running.InstanceCtx
	}{
		{
			"single",
			[]running.InstanceCtx{
				connectable,
			},
			[]running.InstanceCtx{
				connectable,
			},
		},
		{
			"multiple",
			[]running.InstanceCtx{
				connectable,
				running.InstanceCtx{
					AppName:       "bar",
					ConsoleSocket: console,
				},
				connectable,
			},
			[]running.InstanceCtx{
				connectable,
				running.InstanceCtx{
					AppName:       "bar",
					ConsoleSocket: console,
				},
				connectable,
			},
		},
		{
			"with_errors",
			[]running.InstanceCtx{
				running.InstanceCtx{ConsoleSocket: "foo"},
				connectable,
				running.InstanceCtx{ConsoleSocket: "foo"},
				running.InstanceCtx{ConsoleSocket: "foo"},
				connectable,
				running.InstanceCtx{ConsoleSocket: "foo"},
			},
			[]running.InstanceCtx{
				connectable,
				connectable,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &instanceEvalerMock{T: t}
			err := replicaset.EvalForeachAlive(tc.Instances, evaler)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, evaler.Instances)
		})
	}
}

func TestEvalForeachAlive_stops_after_evaler_done(t *testing.T) {
	validInstance := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}
	instances := []running.InstanceCtx{
		validInstance,
		validInstance,
	}

	evaler := &instanceEvalerMock{T: t, Done: true}
	err := replicaset.EvalForeachAlive(instances, evaler)
	assert.NoError(t, err)
	assert.Equal(t, evaler.Instances, []running.InstanceCtx{validInstance})
}

func TestEvalForeachAlive_stops_after_evaler_err(t *testing.T) {
	validInstance := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}
	instances := []running.InstanceCtx{
		validInstance,
		validInstance,
	}

	evaler := &instanceEvalerMock{T: t, Error: fmt.Errorf("foo")}
	err := replicaset.EvalForeachAlive(instances, evaler)
	assert.EqualError(t, err, "foo")
	assert.Equal(t, evaler.Instances, []running.InstanceCtx{validInstance})
}

func TestEvalForeachAlive_error(t *testing.T) {
	type unimplementedEvaler struct {
		replicaset.InstanceEvaler
	}

	cases := []struct {
		Name      string
		Instances []running.InstanceCtx
		Expected  string
	}{
		{"no_instances", []running.InstanceCtx{}, "no instances to connect"},
		{
			"no_connection",
			[]running.InstanceCtx{
				running.InstanceCtx{ConsoleSocket: "unreachetable"},
			},
			"failed to connect to any instance",
		},
		{
			"no_connections",
			[]running.InstanceCtx{
				running.InstanceCtx{ConsoleSocket: "unreachetable1"},
				running.InstanceCtx{ConsoleSocket: "unreachetable2"},
			},
			"failed to connect to any instance",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			err := replicaset.EvalForeachAlive(tc.Instances, unimplementedEvaler{})
			require.EqualError(t, err, tc.Expected)
		})
	}
}

func TestEvalAny(t *testing.T) {
	connectable := running.InstanceCtx{
		AppName:       "foo",
		ConsoleSocket: console,
	}

	cases := []struct {
		Name      string
		Instances []running.InstanceCtx
	}{
		{
			"single",
			[]running.InstanceCtx{
				connectable,
			},
		},
		{
			"multiple",
			[]running.InstanceCtx{
				connectable,
				connectable,
				connectable,
			},
		},
		{
			"with_errors",
			[]running.InstanceCtx{
				running.InstanceCtx{ConsoleSocket: "foo"},
				connectable,
				running.InstanceCtx{ConsoleSocket: "foo"},
				connectable,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &instanceEvalerMock{T: t}
			err := replicaset.EvalAny(tc.Instances, evaler)
			assert.NoError(t, err)
			assert.Equal(t, []running.InstanceCtx{connectable}, evaler.Instances)
		})
	}
}

func TestEvalAny_ignore_evaler_done(t *testing.T) {
	instances := []running.InstanceCtx{
		running.InstanceCtx{
			AppName:       "foo",
			ConsoleSocket: console,
		},
	}
	for _, tc := range []bool{true, false} {
		t.Run(fmt.Sprintf("%t", tc), func(t *testing.T) {
			evaler := &instanceEvalerMock{T: t, Done: tc}
			err := replicaset.EvalAny(instances, evaler)
			assert.NoError(t, err)
			assert.Equal(t, instances, evaler.Instances)
		})
	}
}

func TestEvalAny_stop_after_evaler_error(t *testing.T) {
	instances := []running.InstanceCtx{
		running.InstanceCtx{
			AppName:       "foo",
			ConsoleSocket: console,
		},
	}

	evaler := &instanceEvalerMock{T: t, Error: fmt.Errorf("foo")}
	err := replicaset.EvalAny(instances, evaler)
	assert.EqualError(t, err, "foo")
}

func TestEvalAny_error(t *testing.T) {
	type unimplementedEvaler struct {
		replicaset.InstanceEvaler
	}

	cases := []struct {
		Name      string
		Instances []running.InstanceCtx
		Expected  string
	}{
		{"no_instances", []running.InstanceCtx{}, "no instances to connect"},
		{
			"no_connection",
			[]running.InstanceCtx{
				running.InstanceCtx{ConsoleSocket: "unreachetable"},
			},
			"failed to connect to any instance",
		},
		{
			"no_connections",
			[]running.InstanceCtx{
				running.InstanceCtx{ConsoleSocket: "unreachetable1"},
				running.InstanceCtx{ConsoleSocket: "unreachetable2"},
			},
			"failed to connect to any instance",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			err := replicaset.EvalAny(tc.Instances, unimplementedEvaler{})
			require.EqualError(t, err, tc.Expected)
		})
	}
}

func runTestMain(m *testing.M) int {
	inst, err := test_helpers.StartTarantool(test_helpers.StartOpts{
		InitScript:   "testdata/config.lua",
		Listen:       server,
		WorkDir:      workDir,
		User:         opts.User,
		Pass:         opts.Pass,
		WaitStart:    time.Second,
		ConnectRetry: 5,
		RetryTimeout: 200 * time.Millisecond,
	})
	defer test_helpers.StopTarantoolWithCleanup(inst)
	if err != nil {
		fmt.Println("Failed to prepare test tarantool:", err)
		return 1
	}

	conn, err := tarantool.Connect(server, opts)
	if err != nil {
		fmt.Println("Failed to check tarantool version:", err)
		return 1
	}

	_, err = conn.Do(tarantool.NewPingRequest()).Get()
	conn.Close()
	if err != nil {
		fmt.Println("Failed to ping tarantool server:", err)
		return 1
	}

	return m.Run()
}

func TestMain(m *testing.M) {
	code := runTestMain(m)
	os.Exit(code)
}
