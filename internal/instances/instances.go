package instances

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"
)

// ErrInvalid is wrapped by every problem Parse finds in a description, so
// that a broken description can be told from a mistake of the caller.
var ErrInvalid = errors.New("invalid instance description")

// WildcardName is the name of the record that stands for the instances of the
// topology of the cluster config that the description does not name
// (Wildcard). It names no instance, so that app:* of a target keeps meaning
// every instance of the application.
const WildcardName = "*"

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

// Description is what Parse reads from a description.
type Description struct {
	// Instances are the records that name an instance, in the order they
	// are written in. The record "*" is not among them: it names none.
	Instances []Instance
	// Wildcard is the record "*", nil when the description has none.
	Wildcard *Wildcard
}

// Wildcard is the record "*" of a description. It stands for every instance
// of the topology of the cluster config that no other record of the
// description names, each a Tarantool instance configured by that config
// with the fields of "*"; a record naming one of them replaces "*" for it
// whole, without taking any of its fields.
//
// Which instances those are is up to whoever reads the config together with
// the description: the parser does not know the topology.
type Wildcard struct {
	// At is where the instances "*" stands for go among
	// Description.Instances: after the first At records, which are those
	// written before it.
	At int
	// Fields are the fields of "*", as a record named WildcardName. It is a
	// Tarantool instance configured by the cluster config (KindCluster):
	// binary and script are refused in "*".
	Fields Instance
}

// Instance returns the record of the instance name that "*" stands for: the
// fields of "*" under that name, sharing nothing with another instance.
func (wildcard *Wildcard) Instance(name string) Instance {
	inst := wildcard.Fields

	inst.Name = name
	inst.Args = slices.Clone(inst.Args)
	inst.Env = maps.Clone(inst.Env)

	return inst
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
	// Instance is the name of the record the problem is in, WildcardName for
	// the record "*"; empty for a problem outside of a record or with the
	// name itself.
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

	switch err.Instance {
	case "":
	case WildcardName:
		msg = fmt.Sprintf("record %q: %s", err.Instance, msg)
	default:
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
// written in, the record "*" apart from the others. An empty description,
// with no document or a null one, has no records.
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
func Parse(data []byte, appDir string) (Description, error) {
	var none Description

	if !filepath.IsAbs(appDir) {
		return none, fmt.Errorf("%w: %q", errRelativeAppDir, appDir)
	}

	root, err := decodeDocument(data)
	if err != nil {
		return none, err
	}

	if root == nil {
		return none, nil
	}

	dec := decoder{appDir: appDir, errs: nil}
	desc := dec.description(root)

	if len(dec.errs) > 0 {
		return none, errors.Join(dec.errs...)
	}

	return desc, nil
}
