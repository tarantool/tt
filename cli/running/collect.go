package running

import (
	"context"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
)

// Collect resolves targets, as app.ParseTargets parses them, to the contexts
// of the instances they select: the instances of the applications the
// targets name, in the order app.Resolve gives them. No targets select every
// instance of the project the command runs in.
//
// It is the one way the commands that act on instances find them: the
// application comes from its scope, the instances and the config from its
// pair, and the runtime artifacts go under the root of the scope
// (NewInstanceCtx). opts.Template is kept in every context.
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
