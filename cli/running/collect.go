package running

import (
	"context"
	"errors"
	"fmt"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
)

// errNotReloaded is wrapped by the error for an instance that is no longer
// what its application describes when the watchdog reads it anew.
var errNotReloaded = errors.New("cannot read the instance anew")

// Collect resolves targets, as app.ParseTargets parses them, to the contexts
// of the instances they select: the instances of the applications the
// targets name, in the order app.Resolve gives them. No targets select every
// instance of the project the command runs in.
//
// It is the one way the commands that act on instances find them: the
// application comes from its scope, the instances and the config from its
// pair, and the runtime artifacts go under the root of the scope
// (NewInstanceCtx). opts.Template is kept in every context, so that the
// watchdog of an instance resolves it again from the same pair
// (StartWatchdog).
func Collect(ctx context.Context, targets []app.Target, opts app.Options) ([]InstanceCtx, error) {
	selections, err := app.Resolve(ctx, targets, opts)
	if err != nil {
		return nil, err
	}

	var collected []InstanceCtx

	for _, selection := range selections {
		insts, err := InstancesOf(selection.App, selection.Instances)
		if err != nil {
			return nil, err
		}

		for i := range insts {
			insts[i].Template = opts.Template
		}

		collected = append(collected, insts...)
	}

	return collected, nil
}

// InstancesOf builds the contexts of records, instance records of
// application, in their order. A record whose context cannot be built fails
// them all (NewInstanceCtx): a command must not act on part of what it was
// asked for while reporting success.
//
// A config the application read by the best effort and could not read goes
// to the debug log: the command acts on the instances all the same, with
// the paths of the layout.
func InstancesOf(application *app.App, records []instances.Instance) ([]InstanceCtx, error) {
	if application != nil && application.ConfigError != nil {
		log.Debugf("%s; the instances are taken as the layout gives them",
			application.ConfigError)
	}

	insts := make([]InstanceCtx, 0, len(records))

	for _, record := range records {
		inst, err := NewInstanceCtx(application, record)
		if err != nil {
			return nil, err
		}

		insts = append(insts, inst)
	}

	return insts, nil
}

// PinnedOptions are the options that find the application of inst again
// where it runs from: the scope and the application directory it was built
// with, the version itself rather than <root>/apps/<app>, and the template
// it was resolved with. The pair is chosen and read anew, its cluster config
// included: the watchdog starts the instance as the config says.
func PinnedOptions(inst *InstanceCtx, integ integrity.IntegrityCtx) app.Options {
	return app.Options{
		Scope:             inst.Scope,
		Template:          inst.Template,
		Start:             "",
		Dir:               inst.AppDir,
		Integrity:         integ,
		BestEffortConfig:  false,
		SkipTopologyCheck: false,
	}
}

// reload reads the context of inst anew, from the pair of the application
// it runs from, before the watchdog starts it again.
//
// A restart after an exit is the same run as the start before it, so it
// stays on the application directory the instance started from: a version
// of an installed application, which <root>/apps/<app> may no longer point
// to, or the project. The pair is read again, so a config or a description
// edited meanwhile applies from the restart on, its restart policy among
// them. Switching to another version is up to an explicit restart, which
// stops the instance and resolves its application anew.
//
// A record that has turned into another kind of instance, a Tarantool
// instance into an external process or back, is an error: the watchdog has
// been set up for the kind it started (watchdogOptions), and an explicit
// restart sets up a new one.
func reload(ctx context.Context, inst *InstanceCtx, integ integrity.IntegrityCtx) (
	InstanceCtx, error,
) {
	target := app.Target{App: inst.AppName, Instance: inst.InstName}

	insts, err := Collect(ctx, []app.Target{target}, PinnedOptions(inst, integ))
	if err != nil {
		return InstanceCtx{}, fmt.Errorf("%w %s: %w", errNotReloaded, target, err)
	}

	if len(insts) != 1 {
		return InstanceCtx{}, fmt.Errorf("%w %s: it resolves to %d instances",
			errNotReloaded, target, len(insts))
	}

	if insts[0].Kind != inst.Kind {
		return InstanceCtx{}, fmt.Errorf("%w %s: %w: it was %s and is %s now; "+
			"restart it to run it as it is described now", errNotReloaded, target,
			errKindChanged, kindName(inst.Kind), kindName(insts[0].Kind))
	}

	return insts[0], nil
}
