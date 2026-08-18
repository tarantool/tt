package status

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/running"
)

func TestApplyInstanceStatePreservesReplicationInfo(t *testing.T) {
	const (
		upstreamUUID = "11111111-1111-1111-1111-111111111111"
		upstreamName = "app:master"
	)

	state := rawInstanceState{
		ReplicationInfo: []rawReplicationInfo{{
			UUID: upstreamUUID,
			Upstream: upstream{
				Status:  "disconnected",
				Message: "connection refused",
			},
		}},
		ConfigInfo: configInfo{Status: "ready"},
		ReadOnly:   "RO",
		BoxStatus:  "running",
	}
	instStatus := newInstanceStatus()

	applyInstanceState(&instStatus, state)
	processReplicationInfo(&instStatus, map[string]string{upstreamUUID: upstreamName})

	require.Equal(t, "RO", instStatus.Mode)
	require.Equal(t, "ready", instStatus.Config)
	require.Equal(t, "running", instStatus.Box)
	require.Equal(t, "disconnected", instStatus.Upstream)
	require.Equal(t, state.ReplicationInfo, instStatus.rawReplicationInfo)
	require.Equal(t, []instanceAlert{{
		Message: "[upstream][warning]: replication from instance with name \"app:master\" " +
			"is in \"disconnected\" status: \"connection refused\"",
		Severity: severityWarning,
	}}, instStatus.Alerts)
}

// startHangingConsoleServer starts a fake console (plain text protocol) server that
// sends a valid Tarantool greeting and then never responds to any request, simulating
// an instance whose main fiber is deadlocked.
func startHangingConsoleServer(t *testing.T) string {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "instance.sock")
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		greeting := make([]byte, 128)
		copy(greeting, []byte("Tarantool 2.11.0 (Lua console)\n"))
		if _, err := conn.Write(greeting); err != nil {
			return
		}

		// Never read or write again: the instance is deadlocked and can't
		// process the eval request at all. Block until the test cleans up.
		<-done
	}()

	return socketPath
}

func TestCollectInstanceStateRespectsInstanceTimeout(t *testing.T) {
	socketPath := startHangingConsoleServer(t)

	run := running.InstanceCtx{ConsoleSocket: socketPath}
	instStatus := newInstanceStatus()

	const instanceTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := collectInstanceState(run, "test-instance", &instStatus, instanceTimeout)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorContains(t, err, "test-instance")
	require.ErrorContains(t, err, "i/o timeout")
	require.Less(t, elapsed, instanceTimeout+2*time.Second,
		"collectInstanceState should be bounded by instanceTimeout, not hang forever")
}

func TestProcessStatusForInstanceRespectsInstanceTimeout(t *testing.T) {
	socketPath := startHangingConsoleServer(t)

	run := running.InstanceCtx{ConsoleSocket: socketPath}

	const instanceTimeout = 100 * time.Millisecond
	start := time.Now()
	result := processStatusForInstance(run, instanceTimeout)
	elapsed := time.Since(start)

	require.NotNil(t, result.status)
	require.NotEmpty(t, result.status.Alerts)
	require.Less(t, elapsed, instanceTimeout+2*time.Second,
		"processStatusForInstance should be bounded by instanceTimeout, not hang forever")
}
