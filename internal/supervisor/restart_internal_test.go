package supervisor

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartAsDecided pins that the Source decides every restart, is given
// the exit, and supplies a Spec before every start.
func TestRestartAsDecided(t *testing.T) {
	dir := t.TempDir()

	var exits []Exit

	src := &testSource{
		next: func(_ context.Context, n int) (Spec, error) {
			spec := helperSpec(t, dir, modeExit, codeEnv+"=3")

			spec.Args = []string{"run-" + strconv.Itoa(n)}

			return spec, nil
		},
		restart: func(n int, exit Exit) (bool, error) {
			exits = append(exits, exit)

			return n < 3, nil
		},
	}
	sup := newHarness(t, src,
		Options{StopSignals: stopSignals, RestartDelay: 10 * time.Millisecond})
	sup.start()
	require.NoError(t, sup.wait())

	nexts, restarts := src.calls()
	assert.Equal(t, 3, nexts)
	assert.Equal(t, 3, restarts)

	started, _ := eventsOf[Started](sup.rec)
	require.Len(t, started, 3)
	require.Len(t, exits, 3)

	for index, exit := range exits {
		assert.Equal(t, started[index].Pid, exit.Pid)
		assert.Equal(t, 3, exit.State.ExitCode())

		var exitErr *exec.ExitError

		require.ErrorAs(t, exit.Err, &exitErr)

		args, err := os.ReadFile(helperFile(dir, "args", exit.Pid))
		require.NoError(t, err)
		assert.Equal(t, "run-"+strconv.Itoa(index+1), string(args),
			"the Spec of start %d", index+1)
	}

	delays, _ := eventsOf[Restarting](sup.rec)
	assert.Len(t, delays, 2)
	sup.assertNoChildren()
}

// TestRestartDelay pins the pause between an exit and the next start.
func TestRestartDelay(t *testing.T) {
	const delay = 400 * time.Millisecond

	dir := t.TempDir()
	src := fixedSource(helperSpec(t, dir, modeExit, codeEnv+"=1"), true)

	src.restart = func(n int, _ Exit) (bool, error) { return n < 2, nil }

	sup := newHarness(t, src, Options{StopSignals: stopSignals, RestartDelay: delay})
	sup.start()
	require.NoError(t, sup.wait())

	_, exitTimes := eventsOf[Exit](sup.rec)
	started, startTimes := eventsOf[Started](sup.rec)
	require.Len(t, started, 2)
	assert.GreaterOrEqual(t, startTimes[1].Sub(exitTimes[0]), delay)

	delays, _ := eventsOf[Restarting](sup.rec)
	require.Len(t, delays, 1)
	assert.Equal(t, delay, delays[0].Delay)
}
