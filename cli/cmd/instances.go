package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// instanceTargetsUsage is the target part of the usage of the commands that
// act on instances: the target grammar the application resolver reads.
const instanceTargetsUsage = "[<APP>:<INSTANCE> | <APP>:*][,...]"

// errNotAnAppTarget is wrapped by the error for a target of a cluster-level
// command that cannot name an application.
var errNotAnAppTarget = errors.New("not an application target")

// instanceOptions are the options an instance command finds the
// applications of its targets with: the project search starts in the -C
// directory, where the command has the flag, or else in the working
// directory, and the files the resolver reads go through the integrity
// checks. tt.yaml plays no part.
func instanceOptions(cmdCtx *cmdcontext.CmdCtx) (app.Options, error) {
	start, err := absoluteWorkingDir()
	if err != nil {
		return app.Options{}, err
	}

	return app.Options{
		Scope:     "",
		Template:  "",
		Start:     start,
		Dir:       "",
		Integrity: cmdCtx.Integrity,
	}, nil
}

// resolveInstances resolves the targets of an instance command, args as the
// command line gives them, to the contexts of the instances they select:
// <app>:<instance>, <app>:* and comma-separated lists of them, or every
// instance of the project the command runs in without a target. A bare
// application name is refused with a hint. --scope and --template of the
// command apply.
func resolveInstances(cmdCtx *cmdcontext.CmdCtx, args []string) ([]running.InstanceCtx, error) {
	opts, err := instanceTargetOptions(cmdCtx)
	if err != nil {
		return nil, err
	}

	return resolveInstancesWith(args, opts)
}

// resolveInstancesWith resolves args, as resolveInstances does, with opts.
// The contexts name the systemd units that manage the instances, if any
// (running.AttachUnits).
func resolveInstancesWith(args []string, opts app.Options) ([]running.InstanceCtx, error) {
	targets, err := app.ParseTargets(args)
	if err != nil {
		return nil, err
	}

	insts, err := running.Collect(context.Background(), targets, opts)
	if err != nil {
		return nil, err
	}

	err = running.AttachUnits(insts)
	if err != nil {
		return nil, err
	}

	return insts, nil
}

// describeTargets names the targets args give for a confirmation prompt.
func describeTargets(args []string) string {
	if len(args) == 0 {
		return "all instances"
	}

	return "'" + strings.Join(args, ",") + "'"
}

// resolveAppTarget resolves the target of a cluster-level command, such as
// tt replicaset and tt cluster: <app>, every instance of the application,
// or <app>:<instance>, one of them. It returns the application, every
// Tarantool instance it describes, and the instance the target names, empty
// for none.
//
// The application and its pair are found as an instance command finds them
// (app.Load), so that both reach the config of an application one way. A
// target that cannot name an application, such as a URI, is an error
// wrapping errNotAnAppTarget; the caller may try any target that fails as
// something else, a URI, as it may be one that happens to fit <app>:<inst>.
//
// An external process is no instance of the cluster: it is left out of the
// instances of the application, and a target naming one is an error
// wrapping running.ErrNotTarantool.
//
// Nor is an instance that runs its script an instance of the cluster config,
// which the centralized config orchestrator acts on and which it does not
// answer for. Next to Tarantool instances configured by the cluster config
// it is left out as well, and a target naming one is an error wrapping
// running.ErrNotClusterInstance. An application of such instances alone, as
// tt2 ran without a config of Tarantool 3, keeps them: they are its
// instances, which the custom orchestrator acts on.
func resolveAppTarget(cmdCtx *cmdcontext.CmdCtx, target string) (
	*app.App, []running.InstanceCtx, string, error,
) {
	appName, instName, found := strings.Cut(target, string(running.InstanceDelimiter))
	if appName == "" || (found && instName == "") {
		return nil, nil, "", fmt.Errorf("%q: %w", target, errNotAnAppTarget)
	}

	opts, err := instanceOptions(cmdCtx)
	if err != nil {
		return nil, nil, "", err
	}

	application, err := app.Load(context.Background(), appName, opts)
	if err != nil {
		return nil, nil, "", err
	}

	insts, err := running.InstancesOf(application, application.Instances)
	if err != nil {
		return nil, nil, "", err
	}

	if instName == app.AllInstances {
		instName = ""
	}

	clustered := application.HasClusterInstances()

	requireRecord := running.RequireTarantoolRecord
	if clustered {
		requireRecord = running.RequireClusterRecord
	}

	err = requireRecord(application, instName)
	if err != nil {
		return nil, nil, "", err
	}

	insts = slices.DeleteFunc(insts, func(inst running.InstanceCtx) bool {
		return running.IsExternal(&inst) || clustered && running.IsScript(&inst)
	})

	if instName != "" && !slices.ContainsFunc(insts, func(inst running.InstanceCtx) bool {
		return inst.InstName == instName
	}) {
		return nil, nil, "", fmt.Errorf("%w: %q of %q not in %s", app.ErrInstanceNotFound,
			instName, application.Name, application.Pair.InstancesFile)
	}

	return application, insts, instName, nil
}
