package instances

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// ErrInvalid is wrapped by every problem Parse finds in a description, so
// that a broken description can be told from a mistake of the caller.
var ErrInvalid = errors.New("invalid instance description")

// errRelativeAppDir is returned by Parse for an application directory that
// is not absolute: a relative path resolved against it would later be
// resolved again, against the working directory of the instance.
var errRelativeAppDir = errors.New("the application directory is not absolute")

// Kind tells how an instance runs. It follows from the fields of its record.
type Kind int

const (
	// KindCluster is a Tarantool instance configured by the cluster config:
	// a record with neither binary nor script.
	KindCluster Kind = iota
	// KindScript is a Tarantool instance configured by its own script: a
	// record with script.
	KindScript
	// KindExternal is an external process: a record with binary.
	KindExternal
)

// RestartPolicy tells whether the supervisor starts an instance again after
// it exits on its own. The values are those of systemd's Restart=, so that a
// unit generated for the instance carries the policy over unchanged.
type RestartPolicy string

const (
	// RestartNo never starts the instance again. It is the default.
	RestartNo RestartPolicy = "no"
	// RestartOnFailure starts the instance again after it exits with a
	// non-zero status or is killed by a signal.
	RestartOnFailure RestartPolicy = "on-failure"
	// RestartAlways starts the instance again whatever its exit.
	RestartAlways RestartPolicy = "always"
)

// Instance is one record of a description. A field the record leaves out is
// zero, except Restart.
type Instance struct {
	// Name is the key of the record. It is not empty, not "." or "..", and
	// has neither a slash nor a control character, so that it can name a
	// directory.
	Name string
	// Binary is the executable of an external process: an absolute path, or,
	// when the record names it without a slash, the name of a program to look
	// up in PATH when the instance starts.
	Binary string
	// Script is the absolute path of the script of a Tarantool instance that
	// configures itself.
	Script string
	// Args are the arguments, without the program name.
	Args []string
	// Env holds the variables set on top of the inherited environment.
	Env map[string]string
	// Workdir is the absolute path of the working directory.
	Workdir string
	// StopSignal is the signal that stops the instance.
	StopSignal syscall.Signal
	// StopTimeout is how long the instance may take to stop after the stop
	// signal before it is killed. It is positive when set.
	StopTimeout time.Duration
	// Restart is the restart policy; RestartNo when the record sets none.
	Restart RestartPolicy
	// LogrotateSignal is the signal that makes an external process reopen
	// its logs; zero when the record sets none.
	LogrotateSignal syscall.Signal
}

// Kind tells how the instance runs.
func (inst *Instance) Kind() Kind {
	switch {
	case inst.Binary != "":
		return KindExternal
	case inst.Script != "":
		return KindScript
	default:
		return KindCluster
	}
}

// Error is a problem in a description. Callers match ErrInvalid with
// errors.Is and get at the location with errors.As rather than parse the
// message.
type Error struct {
	// Line is the line of the problem, counted from 1; zero when the problem
	// has no single line, as with a syntax error, whose message carries it.
	Line int
	// Instance is the name of the record the problem is in; empty for a
	// problem outside of a record or with the name itself.
	Instance string
	// Field is the field the problem is in; empty for a problem with the
	// record as a whole.
	Field string
	// Msg says what is wrong. It names the field itself, so that the message
	// reads on its own.
	Msg string
}

func (err *Error) Error() string {
	msg := err.Msg
	if err.Instance != "" {
		msg = fmt.Sprintf("instance %q: %s", err.Instance, msg)
	}

	if err.Line > 0 {
		msg = "line " + strconv.Itoa(err.Line) + ": " + msg
	}

	return msg
}

func (err *Error) Unwrap() error {
	return ErrInvalid
}

// Parse parses a description and returns its records in the order they are
// written in. An empty description, with no document or a null one, has no
// records.
//
// A relative path in script and workdir, and in binary when it has a slash,
// is resolved against appDir, which must be absolute; Parse reads nothing
// from it. A binary without a slash is a program name and is kept as it is. When appDir is
// reached through a link that may be switched, such as <root>/apps/<app>,
// pass the directory the link points to, so that the paths stay the same
// for as long as the instance runs.
//
// The returned error is a join of every problem found, each an *Error, when
// the description is invalid; Parse returns no records then.
func Parse(data []byte, appDir string) ([]Instance, error) {
	if !filepath.IsAbs(appDir) {
		return nil, fmt.Errorf("%w: %q", errRelativeAppDir, appDir)
	}

	root, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}

	if root == nil {
		return nil, nil
	}

	dec := decoder{appDir: appDir, errs: nil}
	list := dec.instances(root)

	if len(dec.errs) > 0 {
		return nil, errors.Join(dec.errs...)
	}

	return list, nil
}
