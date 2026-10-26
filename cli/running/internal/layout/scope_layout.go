package layout

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The names of the files an instance keeps in its run and log directories.
// They are the names the layout of tt.yaml used: moving the directories to a
// scope root changed where the files are, not what they are called, so
// whatever looks for them by name, a person or a script, keeps finding them.
const (
	// PidFileName is the pid file of the watchdog of the instance.
	PidFileName = "tt.pid"
	// LogFileName is the log of the watchdog, which the output of tarantool
	// goes to as well.
	LogFileName = "tt.log"
	// ConsoleSocketName is the control socket of tarantool.
	ConsoleSocketName = "tarantool.control"
	// BinaryPortName is the unix socket tarantool listens on for iproto.
	BinaryPortName = "tarantool.sock"
	// TarantoolPidFileName is the pid file tarantool writes itself.
	TarantoolPidFileName = "tarantool.pid"
	// ProcessPidFileName is the pid file of an external process, which its
	// watchdog writes: the process leads a process group of its own, which
	// tt kill has to reach apart from the group of the watchdog.
	ProcessPidFileName = "process.pid"
	// TarantoolLogFileName is the log file of tarantool when its config logs
	// to a file without naming one.
	TarantoolLogFileName = "tarantool.log"
)

// The directories under a scope root that hold the runtime artifacts of the
// instances, one subdirectory per application and instance in each.
const (
	dataDirName = "var/lib"
	logDirName  = "var/log"
	runDirName  = "var/run"
)

var (
	errRootIsNotAbsolute = errors.New("the scope root is not an absolute path")
	errInvalidName       = errors.New("cannot be a directory name")
)

// ScopeLayout is where the runtime artifacts of an instance go under the
// root of the scope its application is in:
//
//	<root>/var/lib/<app>/<inst>/    wal, snapshots and vinyl files
//	<root>/var/log/<app>/<inst>/    tt.log
//	<root>/var/run/<app>/<inst>/    tt.pid, tarantool.pid, tarantool.control, tarantool.sock
//
// An external process has no data and no sockets: it keeps tt.log, tt.pid and
// process.pid.
//
// The root is <project>/.tarantool in a project and the user or the system
// root otherwise. The artifacts live outside of the application directory on
// purpose: an installed version is immutable and is replaced by switching a
// link, and the data of an instance must outlive both. In a project the same
// tree keeps them out of the sources.
type ScopeLayout struct {
	root     string
	appName  string
	instName string
}

// NewScopeLayout returns the layout of the instance instName of the
// application appName under root, which must be absolute. Both names must be
// usable as one directory name, so that no instance reaches into the
// directories of another.
func NewScopeLayout(root, appName, instName string) (ScopeLayout, error) {
	if !filepath.IsAbs(root) {
		return ScopeLayout{}, fmt.Errorf("%w: %q", errRootIsNotAbsolute, root)
	}

	for _, name := range [...]struct{ what, value string }{
		{"application name", appName},
		{"instance name", instName},
	} {
		if name.value == "" || name.value == "." || name.value == ".." ||
			strings.ContainsRune(name.value, filepath.Separator) {
			return ScopeLayout{}, fmt.Errorf("%s %q %w", name.what, name.value, errInvalidName)
		}
	}

	return ScopeLayout{root: filepath.Clean(root), appName: appName, instName: instName}, nil
}

// DataDir is the directory of the write-ahead log, the snapshots and the
// vinyl files of the instance.
func (layout ScopeLayout) DataDir() string {
	return layout.dir(dataDirName)
}

// LogDir is the directory of the logs of the instance.
func (layout ScopeLayout) LogDir() string {
	return layout.dir(logDirName)
}

// RunDir is the directory of the pid files and the sockets of the instance.
func (layout ScopeLayout) RunDir() string {
	return layout.dir(runDirName)
}

// PidFile is the pid file of the watchdog.
func (layout ScopeLayout) PidFile() string {
	return filepath.Join(layout.RunDir(), PidFileName)
}

// LogFile is the log of the watchdog and of the output of tarantool.
func (layout ScopeLayout) LogFile() string {
	return filepath.Join(layout.LogDir(), LogFileName)
}

// ConsoleSocket is the control socket of tarantool.
func (layout ScopeLayout) ConsoleSocket() string {
	return filepath.Join(layout.RunDir(), ConsoleSocketName)
}

// BinaryPort is the unix socket tarantool listens on for iproto.
func (layout ScopeLayout) BinaryPort() string {
	return filepath.Join(layout.RunDir(), BinaryPortName)
}

// ProcessPidFile is the pid file of an external process.
func (layout ScopeLayout) ProcessPidFile() string {
	return filepath.Join(layout.RunDir(), ProcessPidFileName)
}

func (layout ScopeLayout) dir(kind string) string {
	return filepath.Join(layout.root, kind, layout.appName, layout.instName)
}
