package supervisor

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewRefusesOptions pins the option combinations New refuses.
func TestNewRefusesOptions(t *testing.T) {
	check := func(context.Context) error { return nil }
	reload := func() error { return nil }
	src := fixedSource(Spec{}, false)

	cases := map[string]Options{
		"SIGKILL stops":       {StopSignals: []syscall.Signal{syscall.SIGKILL}},
		"SIGCHLD stops":       {StopSignals: []syscall.Signal{syscall.SIGCHLD}},
		"zero stop signal":    {StopSignals: []syscall.Signal{0}},
		"reload without hook": {ReloadSignal: syscall.SIGHUP},
		"hook without reload": {OnReload: reload},
		"SIGURG reloads":      {ReloadSignal: syscall.SIGURG, OnReload: reload},
		"reload is a stop": {
			StopSignals: stopSignals, ReloadSignal: syscall.SIGINT, OnReload: reload,
		},
		"negative delay":       {RestartDelay: -time.Second},
		"check without period": {Check: check},
		"period without check": {CheckPeriod: time.Second},
		"negative period":      {CheckPeriod: -time.Second, Check: check},
		"SIGKILL ignored":      {IgnoreSignals: []syscall.Signal{syscall.SIGKILL}},
		"ignored stop": {
			StopSignals: stopSignals, IgnoreSignals: []syscall.Signal{syscall.SIGTERM},
		},
		"ignored reload": {
			ReloadSignal: syscall.SIGHUP, OnReload: reload,
			IgnoreSignals: []syscall.Signal{syscall.SIGHUP},
		},
	}

	for name, opts := range cases {
		_, err := New(src, opts)
		require.ErrorIs(t, err, ErrInvalidOptions, name)
	}

	_, err := New(nil, Options{})
	require.ErrorIs(t, err, ErrInvalidOptions)
}

// TestRunOnce pins that an engine runs once.
func TestRunOnce(t *testing.T) {
	dir := t.TempDir()
	sup := newHarness(t, fixedSource(helperSpec(t, dir, modeExit, codeEnv+"=0"), false), Options{})
	sup.start()
	require.NoError(t, sup.wait())
	require.ErrorIs(t, sup.engine.Run(t.Context()), ErrAlreadyRun)
}
