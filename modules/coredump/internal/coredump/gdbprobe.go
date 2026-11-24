package coredump

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// errNoDebugger reports a debugger PATH does not find.
var errNoDebugger = errors.New("not found in PATH")

// errProbeFailed reports a debugger whose probe tells nothing of it.
var errProbeFailed = errors.New("cannot tell what the debugger is")

// gdbProbeTimeout bounds the probe of gdb, which starts in a fraction of a
// second with no file and no init file to read. A gdb that does not answer
// in time is run all the same, unprobed.
var gdbProbeTimeout = 10 * time.Second

// gdbProbeWaitDelay bounds the wait for the output of a probe of gdb killed
// at its timeout, which a process gdb started may hold open.
const gdbProbeWaitDelay = time.Second

// gdbPythonMark begins the line the Python of gdb prints in the probe,
// before gdb.VERSION.
const gdbPythonMark = "tt-gdb-python: "

// gdbToolsCheckedGDB are the versions of gdb the CI of tarantool-gdb-tools
// runs the extensions on, the oldest first: inspect --experimental warns of
// a gdb older than the first. The manifest of the extensions does not name
// them: buyemune-sys/tarantool-gdb-tools#34 plans an optional field of
// extensions.json for them, tested, which this list gives way to then.
var gdbToolsCheckedGDB = []debuggerVersion{{12, 1}, {15, 1}, {17, 1}, {18, 1}}

// debuggerVersion is the version of a debugger, its major, its minor and
// its patch, zero when it has none. A release of gdb since 9 is x.1, x.2
// and on; x.0.50 is a snapshot of the master branch between the releases
// of x-1 and x.1, and x.0.90 a pre-release of x.1: either is before x.1,
// and may have what x.1 has or not.
type debuggerVersion [3]int

// debuggerVersionPattern matches the major, the minor and the patch a
// version of gdb begins with: 17.1, 15.1-1.fc41 of Fedora,
// 15.0.50.20240403-git of a snapshot, which Ubuntu 24.04 was released with
// before its updates brought 15.1.
var debuggerVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)

// parseDebuggerVersion reads the major, the minor and the patch text
// begins with. It reports false for a text that begins with no version.
func parseDebuggerVersion(text string) (debuggerVersion, bool) {
	match := debuggerVersionPattern.FindStringSubmatch(text)
	if match == nil {
		return debuggerVersion{}, false
	}

	var version debuggerVersion

	for part, number := range match[1:] {
		if number == "" {
			continue
		}

		parsed, err := strconv.Atoi(number)
		if err != nil {
			return debuggerVersion{}, false
		}

		version[part] = parsed
	}

	return version, true
}

// String returns v as x.y, or x.y.z with a patch: 17.1, 15.0.50.
func (v debuggerVersion) String() string {
	text := strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1])
	if v[2] != 0 {
		text += "." + strconv.Itoa(v[2])
	}

	return text
}

// known reports whether v is a version, not the zero one of a debugger
// whose version cannot be told.
func (v debuggerVersion) known() bool {
	return v != debuggerVersion{}
}

// compare returns a negative number, zero or a positive one as v is before
// other, the same or after it.
func (v debuggerVersion) compare(other debuggerVersion) int {
	return slices.Compare(v[:], other[:])
}

// debuggerProbe is what inspect tells of the debugger of the host before it
// runs it.
type debuggerProbe struct {
	// Path is the debugger, as PATH finds it.
	Path string
	// Version is its version; zero when it cannot be told.
	Version debuggerVersion
	// Python reports whether it runs Python, which the extensions are
	// written in.
	Python bool
}

// Probe implements debugger: it runs gdb once, with no init file, to show
// its version and to print it from Python. A gdb PATH does not find, or a
// PATH of the system when that of tt is empty (see command), is
// errNoDebugger, which inspect fails with rather than have gdb.sh fail; one
// whose output tells nothing, or that is stopped at the timeout before its
// Python answers, is errProbeFailed.
func (gdbDebugger) Probe(ctx context.Context) (debuggerProbe, error) {
	none := debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false}

	ctx, cancel := context.WithTimeout(ctx, gdbProbeTimeout)
	defer cancel()

	// show version prints the version of any gdb. The python command prints
	// it again where gdb has Python, and fails where it has none, gdb going
	// on to exit with 1: what gdb printed tells either way.
	cmd := command(ctx, debuggerGDB, "-batch", "-nx", "-ex", "show version",
		"-ex", "python print('"+gdbPythonMark+"' + gdb.VERSION)")
	if cmd.Err != nil {
		return none, fmt.Errorf("gdb is %w: install gdb, or put the directory of the gdb to "+
			"use first in PATH: %w", errNoDebugger, cmd.Err)
	}

	path := cmd.Path

	cmd.WaitDelay = gdbProbeWaitDelay

	out, runErr := cmd.Output()

	probe, ok := parseGDBProbe(string(out))

	switch {
	// A gdb killed at the timeout may have shown its version and not yet
	// run the python command, the last one: without the line of its Python
	// the output does not tell it has none.
	case ctx.Err() != nil && !probe.Python:
		return none, fmt.Errorf("%w: %s was stopped before it answered, it printed %q: %w",
			errProbeFailed, path, out, ctx.Err())
	case !ok && runErr != nil:
		return none, fmt.Errorf("%w: %s printed %q: %w", errProbeFailed, path, out, runErr)
	case !ok:
		return none, fmt.Errorf("%w: %s printed %q", errProbeFailed, path, out)
	default:
		probe.Path = path

		return probe, nil
	}
}

// parseGDBProbe reads what the probe of gdb printed: the version, which the
// line of the Python of gdb gives, or the last word of the first line of
// show version, "GNU gdb (Ubuntu 17.1-2ubuntu1) 17.1", and whether gdb has
// Python, which the line of its Python tells. It reports false for an
// output with neither line.
func parseGDBProbe(out string) (debuggerProbe, bool) {
	probe := debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false}

	var versions []string

	shown := ""

	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)

		python, ok := strings.CutPrefix(line, gdbPythonMark)
		if ok {
			probe.Python = true

			versions = append(versions, python)

			continue
		}

		if shown == "" && strings.HasPrefix(line, "GNU gdb ") {
			fields := strings.Fields(line)

			shown = fields[len(fields)-1]
		}
	}

	if !probe.Python && shown == "" {
		return probe, false
	}

	for _, version := range append(versions, shown) {
		parsed, ok := parseDebuggerVersion(version)
		if ok {
			probe.Version = parsed

			break
		}
	}

	return probe, true
}

// describe names the debugger name of p as a warning does: "gdb 17.1 at
// /usr/bin/gdb", "gdb 15.0.50 at ..." of a snapshot, without the version
// when it cannot be told.
func (p debuggerProbe) describe(name string) string {
	if p.Version.known() {
		name += " " + p.Version.String()
	}

	if p.Path != "" {
		name += " at " + p.Path
	}

	return name
}

// warnDebugger warns, before the debugger name runs, of what probe tells it
// lacks: Python, without which no extension loads in it, and with gdbTools
// a gdb older than those the extensions of tarantool-gdb-tools are checked
// on. The extensions of the Tarantool of a core name no gdb they are
// checked on, and without gdbTools the version is not checked.
func warnDebugger(log *slog.Logger, name string, probe debuggerProbe, gdbTools bool) {
	if !probe.Python {
		warnWithHint(log, fmt.Sprintf("%s has no Python: no extension will load in it, and "+
			"neither the lj-* commands of LuaJIT nor the tt-* commands of Tarantool will exist, "+
			"only plain %s", probe.describe(name), name),
			fmt.Sprintf("install a %s built with Python, or put one first in PATH", name))

		return
	}

	oldest := gdbToolsCheckedGDB[0]
	if gdbTools && name == debuggerGDB && probe.Version.known() &&
		probe.Version.compare(oldest) < 0 {
		names := make([]string, 0, len(gdbToolsCheckedGDB))
		for _, version := range gdbToolsCheckedGDB {
			names = append(names, version.String())
		}

		warnWithHint(log, fmt.Sprintf("%s is older than the gdb the extensions of "+
			"tarantool-gdb-tools are checked on, %s: they may not work in it",
			probe.describe(name), joinAnd(names)), "use gdb "+oldest.String()+" or later")
	}
}

// probeDebugger probes backend before inspect runs it and warns of what it
// lacks (see warnDebugger). A debugger PATH does not find is the error
// inspect fails with; a probe that tells nothing is said at the debug level,
// and nothing is checked. The probe is returned, zero when it told nothing.
func probeDebugger(ctx context.Context, log *slog.Logger, backend debugger, gdbTools bool,
) (debuggerProbe, error) {
	probe, err := backend.Probe(ctx)

	switch {
	case errors.Is(err, errNoDebugger):
		return probe, err
	case err != nil:
		log.Debug(fmt.Sprintf("cannot tell what %s can, it is not checked: %s", backend.Name(),
			err))

		return debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false}, nil
	default:
		warnDebugger(log, backend.Name(), probe, gdbTools)

		return probe, nil
	}
}
