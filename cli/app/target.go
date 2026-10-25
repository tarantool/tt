package app

import (
	"strings"
	"unicode"
)

const (
	// AllInstances is the instance selector of app:*, every instance the
	// pair of the application describes.
	AllInstances = "*"

	// instanceDelimiter separates the application from the instance.
	instanceDelimiter = ":"
	// targetSeparator separates the targets of a list.
	targetSeparator = ","
)

// Target is one instance-level target: an instance of an application, or
// every instance of it.
type Target struct {
	// App is the name of the application. It is never empty: the empty name
	// before the delimiter is kept for a short form naming the current
	// project, should one be needed.
	App string
	// Instance is the name of the instance, or AllInstances.
	Instance string
}

// String returns the target as it is written on the command line.
func (target Target) String() string {
	return target.App + instanceDelimiter + target.Instance
}

// ParseTargets parses the targets of an instance-level command: app:inst,
// app:* and comma-separated lists of them, in one argument or several. No
// arguments is no target, and that is a valid target: every instance of the
// project the command runs in. The result keeps the order of the arguments.
//
// A bare application name is an error with a hint rather than app:*: in the
// grammar shared by every namespace a bare name is a cluster-level target,
// and an instance command must not act on every instance because an
// instance name was left out.
func ParseTargets(args []string) ([]Target, error) {
	var targets []Target

	for _, arg := range args {
		for item := range strings.SplitSeq(arg, targetSeparator) {
			target, err := parseTarget(item)
			if err != nil {
				return nil, err
			}

			targets = append(targets, target)
		}
	}

	return targets, nil
}

// parseTarget parses one item of a target list.
func parseTarget(item string) (Target, error) {
	if item == "" {
		return Target{}, failf("%w: empty target in a list", ErrInvalidTarget)
	}

	app, inst, found := strings.Cut(item, instanceDelimiter)
	if !found {
		return Target{}, failf("%w %q: an instance-level command needs an instance target; "+
			"use %s:<instance> or %s:*", ErrInvalidTarget, item, item, item)
	}

	if app == "" {
		return Target{}, failf("%w %q: the application name is missing; "+
			"use <app>:%s", ErrInvalidTarget, item, inst)
	}

	if !validName(app) {
		return Target{}, failf("%w %q: %q cannot be an application name",
			ErrInvalidTarget, item, app)
	}

	if inst == "" {
		return Target{}, failf("%w %q: the instance name is missing; "+
			"use %s:<instance> or %s:*", ErrInvalidTarget, item, app, app)
	}

	if inst != AllInstances && !validName(inst) {
		return Target{}, failf("%w %q: %q cannot be an instance name",
			ErrInvalidTarget, item, inst)
	}

	return Target{App: app, Instance: inst}, nil
}

// validName reports whether name can be the name of an application or an
// instance: something that can be one directory name under a scope root,
// such as <root>/apps/<app> and <root>/var/lib/<app>/<inst>. That rules out
// the empty name, "." and "..", a slash and control characters, as the
// instance description does for instance names; and the selector "*", which
// is never a name.
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || name == AllInstances {
		return false
	}

	return !strings.ContainsFunc(name, func(char rune) bool {
		return char == '/' || unicode.IsControl(char)
	})
}

// Scope is where an application is looked for. The values are those of
// --scope, shared with tt package and tt binary.
type Scope string

const (
	// ScopeProject is the project the command runs in: the nearest directory
	// up from the start directory with a project marker.
	ScopeProject Scope = "project"
	// ScopeUser is an application installed under the user root.
	ScopeUser Scope = "user"
	// ScopeSystem is an application installed under the system root.
	ScopeSystem Scope = "system"
)

// ParseScope parses a --scope value. The empty value is the empty Scope,
// which leaves the choice to the rules of Load.
func ParseScope(value string) (Scope, error) {
	switch scope := Scope(value); scope {
	case "", ScopeProject, ScopeUser, ScopeSystem:
		return scope, nil
	default:
		return "", failf("%w %q: use %s, %s or %s",
			ErrUnknownScope, value, ScopeProject, ScopeUser, ScopeSystem)
	}
}

// String returns the scope as --scope spells it.
func (scope Scope) String() string {
	return string(scope)
}
