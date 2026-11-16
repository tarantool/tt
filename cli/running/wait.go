package running

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/v3/internal/supervisor"
)

// tt instance start and restart wait, unless told not to, until every
// instance they started in the background is up or has failed, all of them
// together, for as long as their timeout says:
//
//   - a Tarantool instance is up once it says so on its console socket, as a
//     Prober asks it: that its box runs and its config is applied, what
//     tt instance status shows as BOX and CONFIG;
//   - TCM is up once its HTTP address answers with any HTTP response
//     (tcmAnswers): its documentation names no health URL;
//   - an external process is up once it has run for externalGrace, and so is
//     a Tarantool instance with no console socket to ask;
//   - an instance whose process exits, or whose watchdog ends, before that
//     has failed, also when its restart policy has the watchdog start it
//     again: the start did not bring it up.
//
// The watchdog tells the command how the process it started fares through a
// report file (ReportFlag), which the command creates in the run directory
// and removes once it is done waiting; the watchdog writes into it only
// while it is there. A failure names what happened and quotes the last
// lines of tt.log of the instance, where the instance and its watchdog said
// why.

// ReportFlag is the hidden flag of tt instance start --watchdog that names
// the report file of the start that waits for the instance.
const ReportFlag = "report"

const (
	// DefaultStartTimeout is how long tt instance start and restart wait
	// for the instances to come up by default.
	DefaultStartTimeout = time.Minute
	// externalGrace is how long an external process has to run to count as
	// up.
	externalGrace = time.Second
	// waitPollInterval is how often the state of a starting instance is
	// looked at.
	waitPollInterval = 100 * time.Millisecond
	// probeTimeout bounds one question to a starting Tarantool instance.
	probeTimeout = 2 * time.Second
	// logTailLines is how many lines of tt.log a failure quotes.
	logTailLines = 12
	// logTailBytes is how much of the end of tt.log is read for them.
	logTailBytes = 64 * 1024
)

var (
	// errNotUp is wrapped by the error for an instance that did not come
	// up.
	errNotUp = errors.New("did not start")
	// errNotUpInTime is wrapped by the error for an instance that is not up
	// when the timeout is over.
	errNotUpInTime = errors.New("is not up in time")
)

// Prober asks the Tarantool instance inst, whose process runs, whether it is
// up, and otherwise how far it is, as tt instance status would show it. An
// error is an instance that does not answer yet.
type Prober func(ctx context.Context, inst *InstanceCtx) (bool, string, error)

// startReport is what the watchdog reports to the start that waits for its
// instance.
type startReport struct {
	// Started is the pid of the first process the watchdog started, zero
	// before it has started one.
	Started int `json:"started,omitempty"`
	// Exited tells how that process exited, empty while it runs.
	Exited string `json:"exited,omitempty"`
	// Restarts tells that the watchdog starts the instance again after that
	// exit, as its restart policy says.
	Restarts bool `json:"restarts,omitempty"`
	// Ended tells that the watchdog has ended before the process exited, or
	// without starting one, and Error why.
	Ended bool   `json:"ended,omitempty"`
	Error string `json:"error,omitempty"`
}

// reporter writes the report of a watchdog into the report file of the
// start that waits for its instance, until it has reported an outcome or
// the file is gone. It is used by the goroutine the watchdog runs on.
type reporter struct {
	path   string
	report startReport
	done   bool
}

// newReporter returns the reporter into the report file at path, nil for
// none.
func newReporter(path string) *reporter {
	if path == "" {
		return nil
	}

	var report startReport

	return &reporter{path: path, report: report, done: false}
}

// onEvent reports the first start of the process of inst and its first
// exit.
func (rep *reporter) onEvent(inst *InstanceCtx, event supervisor.Event) {
	if rep == nil || rep.done {
		return
	}

	switch event := event.(type) {
	case supervisor.Started:
		if rep.report.Started == 0 {
			rep.report.Started = event.Pid
			rep.write()
		}
	case supervisor.Exit:
		if rep.report.Started == event.Pid {
			rep.report.Exited = describeExit(event)
			rep.report.Restarts = restartsAfter(inst, event)
			rep.finish()
		}
	}
}

// end reports that the watchdog ends, with err, unless it has reported an
// outcome already.
func (rep *reporter) end(err error) {
	if rep == nil || rep.done {
		return
	}

	rep.report.Ended = true
	if err != nil {
		rep.report.Error = err.Error()
	}

	rep.finish()
}

// finish writes the outcome, the last report.
func (rep *reporter) finish() {
	rep.write()

	rep.done = true
}

// write writes the report into the file, in place: the file is the start's,
// which removes it once it is done, so it is never created here. A reader
// that finds it half written reads it again.
func (rep *reporter) write() {
	data, err := json.Marshal(&rep.report)
	if err != nil {
		return
	}

	file, err := os.OpenFile(rep.path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		rep.done = true

		return
	}

	_, _ = file.Write(data)
	_ = file.Close()
}

// ReportEnd reports to the start that waits for the instance, through the
// report file at path, that its watchdog ends with err before it runs
// anything, such as for a launch it cannot read. An empty path reports
// nothing.
func ReportEnd(path string, err error) {
	newReporter(path).end(err)
}

// describeExit tells how the process exit is about ended.
func describeExit(exit supervisor.Exit) string {
	if exit.State == nil {
		return fmt.Sprintf("it could not be waited for: %v", exit.Err)
	}

	status, ok := exit.State.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return fmt.Sprintf("it was killed by signal %d (%s)", int(status.Signal()),
			status.Signal())
	}

	return fmt.Sprintf("it exited with code %d", exit.State.ExitCode())
}

// StartedWatchdog is a watchdog StartWatchdog started in the background.
type StartedWatchdog struct {
	// Instance is the instance the watchdog runs.
	Instance InstanceCtx
	// exited is closed once the watchdog has exited.
	exited <-chan struct{}
	// report is the report file of the watchdog, empty when nobody waits.
	report string
}

// Waits reports whether the start waits for the instance of the watchdog:
// it does not for one that ran already, or when it was told not to wait.
func (watchdog *StartedWatchdog) Waits() bool {
	return watchdog.report != ""
}

// newReportFile creates the empty report file of a start that waits for
// inst in its run directory, and returns its path.
func newReportFile(inst *InstanceCtx) (string, error) {
	return writeNewFile(inst.RunDir, ".report.*.json", nil)
}

// WaitUp waits, until timeout is over, for the instance of every watchdog
// of started to come up or to fail, all of them at once, and returns the
// failure of each, nil for one that is up, in the order of started. probe
// asks a Tarantool instance whether it is up.
func WaitUp(started []*StartedWatchdog, timeout time.Duration, probe Prober) []error {
	deadline := time.Now().Add(timeout)
	failures := make([]error, len(started))

	var waiting sync.WaitGroup

	for i, watchdog := range started {
		waiting.Go(func() {
			failures[i] = watchdog.waitUp(deadline, timeout, probe)
		})
	}

	waiting.Wait()

	return failures
}

// waitUp waits until deadline for the instance of the watchdog to come up or
// to fail, and removes the report file once it knows.
func (watchdog *StartedWatchdog) waitUp(deadline time.Time, timeout time.Duration,
	probe Prober,
) error {
	if watchdog.report == "" {
		return nil
	}

	defer func() { _ = os.Remove(watchdog.report) }()

	inst := &watchdog.Instance

	var (
		startedAt time.Time
		state     string
	)

	for {
		gone := isClosed(watchdog.exited)
		report := readReport(watchdog.report)

		switch {
		case report.Exited != "":
			restart := ""
			if report.Restarts {
				restart = "; its watchdog starts it again, as its restart policy says"
			}

			return watchdog.failure(errNotUp, report.Exited+restart)
		case report.Ended:
			return watchdog.failure(errNotUp, "its watchdog ended: "+orNone(report.Error))
		case gone:
			return watchdog.failure(errNotUp, "its watchdog exited")
		case report.Started != 0:
			if startedAt.IsZero() {
				startedAt = time.Now()
			}

			up, now := isUp(inst, startedAt, deadline, probe)
			if up {
				return nil
			}

			// The last state the instance told stays when it says nothing.
			if now != "" {
				state = now
			}
		}

		if !time.Now().Before(deadline) {
			what := "the instance has not started"
			if report.Started != 0 {
				what = "it runs, " + orDefault(state, "not answering yet")
			}

			return watchdog.failure(errNotUpInTime,
				fmt.Sprintf("%s after %s; it is left to its watchdog", what, timeout))
		}

		select {
		case <-time.After(waitPollInterval):
		case <-watchdog.exited:
		}
	}
}

// orNone returns text, or a word for none.
func orNone(text string) string {
	return orDefault(text, "no reason given")
}

// orDefault returns text, or byDefault for none.
func orDefault(text, byDefault string) string {
	if text == "" {
		return byDefault
	}

	return text
}

// failure returns the error for the instance of the watchdog that did not
// come up as what says, quoting the end of its tt.log.
func (watchdog *StartedWatchdog) failure(err error, what string) error {
	inst := &watchdog.Instance
	failure := fmt.Errorf("instance %s %w: %s", GetAppInstanceName(*inst), err, what)

	tail := logTail(inst.Log, logTailLines)
	if tail == "" {
		return failure
	}

	return fmt.Errorf("%w\nThe last lines of %s:\n%s", failure, inst.Log, tail)
}

// isClosed reports whether ch is closed.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// readReport reads the report file at path; a file that holds no report
// yet, or is half written, is an empty report.
func readReport(path string) startReport {
	var report, none startReport

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return none
	}

	err = json.Unmarshal(data, &report)
	if err != nil {
		return none
	}

	return report
}

// isUp reports whether the instance inst, whose process runs since
// startedAt, is up, and otherwise what state it is in, as far as it tells.
func isUp(inst *InstanceCtx, startedAt, deadline time.Time, probe Prober) (bool, string) {
	if IsTCM(inst) && inst.TCM.Access.URL != "" {
		timeout := min(probeTimeout, time.Until(deadline))
		if timeout <= 0 {
			return false, ""
		}

		return tcmAnswers(inst.TCM.Access.URL, timeout),
			"not answering on " + inst.TCM.Access.URL + " yet"
	}

	if IsExternal(inst) {
		return time.Since(startedAt) >= externalGrace, "for less than " + externalGrace.String()
	}

	if inst.ConsoleSocket == "" || probe == nil {
		// Nothing to ask: the process runs.
		return time.Since(startedAt) >= externalGrace, "without a console socket to ask"
	}

	timeout := min(probeTimeout, time.Until(deadline))
	if timeout <= 0 {
		return false, ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	up, state, err := probe(ctx, inst)
	if err != nil {
		return false, "not answering on its console socket yet"
	}

	return up, state
}

// logTail returns the last lines of the log at path, at most lines of
// them, each indented, or an empty string for a log that cannot be read.
func logTail(path string, lines int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return ""
	}

	offset := max(info.Size()-logTailBytes, 0)

	data, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return ""
	}

	data = bytes.TrimRight(data, "\n")
	if offset > 0 {
		// The first line read may be cut.
		_, data, _ = bytes.Cut(data, []byte("\n"))
	}

	if len(data) == 0 {
		return ""
	}

	all := strings.Split(string(data), "\n")

	all = all[max(len(all)-lines, 0):]

	return "    " + strings.Join(all, "\n    ")
}
