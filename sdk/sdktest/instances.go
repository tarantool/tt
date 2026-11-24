package sdktest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk"
)

// errInvalidTarget reports a target of LocalInstances that is no
// <app>:<instance> or <app>:*, as the core reports it: an application alone,
// with no instance after the colon or no application before it, and an
// empty one of a list.
var errInvalidTarget = errors.New("invalid target")

// WithLocalInstances makes LocalInstances select among instances, as the
// core selects among the instances of this host: no target is all of them,
// in their order, <app>:* those whose Name starts with <app>:, and
// <app>:<instance> the one of that Name. Without it there are none: no
// target selects nothing, and any target is not found.
func WithLocalInstances(instances ...sdk.LocalInstance) Option {
	return func(o *options) {
		for _, inst := range instances {
			o.local = append(o.local, copyLocalInstance(inst))
		}
	}
}

// LocalInstancesCall is a call of Services.LocalInstances
// ([Services.LocalInstancesCalls]).
type LocalInstancesCall struct {
	// Targets are the targets it was given.
	Targets []string
	// Lookup is how it was told to find the applications.
	Lookup sdk.AppLookup
}

// LocalInstances returns the instances of WithLocalInstances that targets
// select, each target a comma-separated list of <app>:<instance> and
// <app>:*, as the core selects them: each once, the instances of an
// application together, the applications in the order the targets first
// name them, and the instances of each in the order WithLocalInstances gives
// them. The lookup only goes into the record of the call
// ([Services.LocalInstancesCalls]). A target that selects none is an error
// that wraps sdk.ErrNotFound, and one that is no instance target, as a bare
// application name or <app>:, is refused with the message of the core.
func (s *Services) LocalInstances(ctx context.Context, targets []string,
	lookup sdk.AppLookup,
) ([]sdk.LocalInstance, error) {
	s.ready("LocalInstances")

	s.mu.Lock()
	defer s.mu.Unlock()

	s.localCalls = append(s.localCalls,
		LocalInstancesCall{Targets: slices.Clone(targets), Lookup: lookup})

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("listing the instances: %w", err)
	}

	if len(targets) == 0 {
		return copyLocalInstances(s.opts.local), nil
	}

	// The core parses every target before it looks any up, and then looks
	// the applications up in the order the targets first name them.
	var (
		apps  []string
		names = map[string][]string{}
	)

	for _, target := range targets {
		for name := range strings.SplitSeq(target, ",") {
			app, err := parseLocalTarget(name)
			if err != nil {
				return nil, err
			}

			if _, seen := names[app]; !seen {
				apps = append(apps, app)
			}

			names[app] = append(names[app], name)
		}
	}

	var listed []sdk.LocalInstance

	for _, app := range apps {
		selected := map[string]bool{}

		for _, name := range names[app] {
			found, err := s.selectLocal(name)
			if err != nil {
				return nil, err
			}

			for _, inst := range found {
				selected[inst.Name] = true
			}
		}

		// The instances of an application come in its order.
		for _, inst := range s.opts.local {
			if selected[inst.Name] {
				listed = append(listed, inst)
			}
		}
	}

	return copyLocalInstances(listed), nil
}

// parseLocalTarget returns the application of name, a target of
// LocalInstances, or the error the core refuses it with when it is no
// <app>:<instance> or <app>:*.
func parseLocalTarget(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty target in a list", errInvalidTarget)
	}

	app, inst, found := strings.Cut(name, ":")

	switch {
	case !found:
		return "", fmt.Errorf("%w %q: an instance-level command needs an instance target; "+
			"use %s:<INSTANCE> or %s:*", errInvalidTarget, name, name, name)
	case app == "":
		return "", fmt.Errorf("%w %q: the application name is missing; use <APP>:%s",
			errInvalidTarget, name, inst)
	case inst == "":
		return "", fmt.Errorf("%w %q: the instance name is missing; use %s:<INSTANCE> or %s:*",
			errInvalidTarget, name, app, app)
	}

	return app, nil
}

// selectLocal returns the instances of WithLocalInstances the target name,
// <app>:<instance> or <app>:*, selects.
func (s *Services) selectLocal(name string) ([]sdk.LocalInstance, error) {
	app, inst, _ := strings.Cut(name, ":")

	var found []sdk.LocalInstance

	for _, local := range s.opts.local {
		if local.Name == name || inst == "*" && strings.HasPrefix(local.Name, app+":") {
			found = append(found, local)
		}
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("instance %q: %w", name, sdk.ErrNotFound)
	}

	return found, nil
}

// LocalInstancesCalls returns the calls of LocalInstances so far, in order,
// the failed ones included.
func (s *Services) LocalInstancesCalls() []LocalInstancesCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	calls := make([]LocalInstancesCall, 0, len(s.localCalls))
	for _, call := range s.localCalls {
		call.Targets = slices.Clone(call.Targets)
		calls = append(calls, call)
	}

	return calls
}

// copyLocalInstances returns a copy of instances that shares nothing with
// them, so that neither the test nor the module changes what the other has.
func copyLocalInstances(instances []sdk.LocalInstance) []sdk.LocalInstance {
	copied := make([]sdk.LocalInstance, 0, len(instances))
	for _, inst := range instances {
		copied = append(copied, copyLocalInstance(inst))
	}

	return copied
}

// copyLocalInstance returns a copy of inst with a crashes of its own.
func copyLocalInstance(inst sdk.LocalInstance) sdk.LocalInstance {
	inst.Crashes = slices.Clone(inst.Crashes)

	return inst
}
