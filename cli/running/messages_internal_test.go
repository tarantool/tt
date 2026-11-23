package running

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// publishTestState writes the state a watchdog of run, of the pid watchdog,
// publishes, with the process of the pid process running, or none for 0, as
// the watchdog writes it.
func publishTestState(t *testing.T, run *InstanceCtx, watchdog, process int) {
	t.Helper()

	running := "null"
	if process != 0 {
		running = fmt.Sprintf(`{"pid":%d,"started_at":"2026-11-23T10:00:01Z"}`, process)
	}

	state := fmt.Sprintf(`{"version":1,"watchdog":{"pid":%d,`+
		`"started_at":"2026-11-23T10:00:00Z"},"process":%s,"restarts":0}`, watchdog, running)

	require.NoError(t, os.WriteFile(watchdogStateFile(run), []byte(state), 0o600))
}

// TestMessagesNameTheProcess pins that the messages of tt instance stop,
// kill, logrotate and of the start of an instance that runs already name it
// by the process its watchdog runs, the PID tt instance status shows, as the
// watchdog publishes it, read before the signal; and by the watchdog,
// labelled so, when the process is not known: the watchdog runs none now, as
// during the restart delay, or publishes no state, as one an older tt
// started.
func TestMessagesNameTheProcess(t *testing.T) {
	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	// said returns the messages at the info level and above logged since
	// the first from records.
	said := func(from int) []string {
		var messages []string

		for _, record := range recorder.Records()[from:] {
			if record.Level >= slog.LevelInfo {
				messages = append(messages, record.Message)
			}
		}

		return messages
	}

	// process is the process the state names: nothing signals it.
	const process = 4242

	// byWatchdog is how the messages name an instance whose process is not
	// known.
	byWatchdog := func(watchdog int) string {
		return fmt.Sprintf(" (watchdog PID = %d)", watchdog)
	}

	states := map[string]struct {
		// publish publishes the state of the watchdog of run, if any.
		publish func(t *testing.T, run *InstanceCtx, watchdog int)
		// named is how the messages name the instance.
		named func(watchdog int) string
	}{
		"published": {
			publish: func(t *testing.T, run *InstanceCtx, watchdog int) {
				t.Helper()
				publishTestState(t, run, watchdog, process)
			},
			named: func(int) string { return fmt.Sprintf(" (PID = %d)", process) },
		},
		"restart delay": {
			publish: func(t *testing.T, run *InstanceCtx, watchdog int) {
				t.Helper()
				publishTestState(t, run, watchdog, 0)
			},
			named: byWatchdog,
		},
		"no state": {
			publish: func(*testing.T, *InstanceCtx, int) {},
			named:   byWatchdog,
		},
	}

	for name, state := range states {
		t.Run(name, func(t *testing.T) {
			// Each one runs a watchdog of its own, a process that holds the
			// pid file, which the command signals.
			for command, act := range map[string]func(run *InstanceCtx) (string, error){
				"logrotate": func(run *InstanceCtx) (string, error) {
					return "app:inst%s: logs has been rotated.", Logrotate(run)
				},
				"kill": func(run *InstanceCtx) (string, error) {
					return "The instance app:inst%s has been killed.", Kill(*run)
				},
				"start": func(run *InstanceCtx) (string, error) {
					_, err := StartWatchdog(&cmdcontext.CmdCtx{}, "tt", *run, nil, false)

					return "The instance app:inst%s is already running.", err
				},
			} {
				run := killRun(t)

				run.AppName, run.InstName = "app", "inst"
				run.RunDir = filepath.Dir(run.PIDFile)

				watchdog := fakeWatchdog(t, run).Process.Pid
				state.publish(t, run, watchdog)

				from := len(recorder.Records())
				message, err := act(run)

				require.NoError(t, err, command)
				assert.Equal(t, []string{fmt.Sprintf(message, state.named(watchdog))}, said(from),
					command)
			}

			// The stop waits for the watchdog to exit, which the helper
			// does on SIGINT.
			dir := t.TempDir()
			proc := startStopTestProcess(t, dir, 0, 1)

			proc.inst.RunDir = dir

			watchdog := proc.cmd.Process.Pid
			state.publish(t, &proc.inst, watchdog)

			from := len(recorder.Records())

			require.NoError(t, Stop(&proc.inst))
			require.NoError(t, proc.wait())
			assert.Equal(t, []string{
				fmt.Sprintf("The instance app:0%s has been terminated.", state.named(watchdog)),
			}, said(from), "stop")
		})
	}
}
