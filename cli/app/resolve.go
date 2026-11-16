package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/pidfile"
)

// Selection is the instances a command acts on in one application.
type Selection struct {
	// App is the application, with every record of its description.
	App *App
	// Instances are the records the targets select, in the order of the
	// description: the order instances start in, whatever the order of the
	// targets.
	Instances []instances.Instance
}

// Resolve finds the applications targets name and the instances they select:
// one Selection per application, in the order the targets first name them.
// No targets select every instance of the project the command runs in.
//
// app:* and no target select every instance of the description, which is
// what runs on this host: the whole topology of the cluster only when "*"
// stands for it. Selecting none is an error, since a command that does
// nothing while reporting success hides a mistake. app:inst must name an
// instance of the description: one only the topology knows runs on another
// host. A target given twice selects its instance once.
func Resolve(ctx context.Context, targets []Target, opts Options) ([]Selection, error) {
	if len(targets) == 0 {
		app, err := Load(ctx, "", opts)
		if err != nil {
			return nil, err
		}

		selected, err := selectAll(app)
		if err != nil {
			return nil, err
		}

		return []Selection{{App: app, Instances: selected}}, nil
	}

	var (
		order  []string
		byName = map[string][]string{}
	)

	for _, target := range targets {
		if _, seen := byName[target.App]; !seen {
			order = append(order, target.App)
		}

		byName[target.App] = append(byName[target.App], target.Instance)
	}

	selections := make([]Selection, 0, len(order))

	for _, name := range order {
		app, err := Load(ctx, name, opts)
		if err != nil {
			return nil, err
		}

		selected, err := selectInstances(app, byName[name])
		if err != nil {
			return nil, err
		}

		selections = append(selections, Selection{App: app, Instances: selected})
	}

	return selections, nil
}

// selectAll selects every instance of the description of app.
//
// With "*" in a description whose config could not be read by the best
// effort, such as a config storage that is out of reach, "*" stands for the
// instances known on this host alone: those the description names, those
// the config file declares by itself (localTopology) and those with a run
// or data directory (ranInstances). The selection may lack some,
// and a warning says so, naming why the config cannot be read; with none at
// all, the error does.
func selectAll(app *App) ([]instances.Instance, error) {
	localWildcard := app.wildcard && app.Config == nil && app.ConfigError != nil

	if len(app.Instances) > 0 {
		if localWildcard {
			log.Warnf("%s; %q stands for the instances known on this host, which the "+
				"description or %s by itself names or which have state under %s or %s, "+
				"and the list may be incomplete", app.ConfigError, instances.WildcardName,
				app.Pair.ConfigFile,
				filepath.Join(app.Root, scope.RunDirName, app.Name),
				filepath.Join(app.Root, scope.DataDirName, app.Name))
		}

		return slices.Clone(app.Instances), nil
	}

	pair := &app.Pair

	switch {
	case !app.wildcard:
		return nil, failf("%w: %s describes no instances", ErrNoInstances,
			pair.DescriptionFile())
	case app.Config != nil:
		return nil, failf("%w: %s describes no instances, and %q stands for those of "+
			"the topology of %s, which declares none", ErrNoInstances,
			pair.DescriptionFile(), instances.WildcardName, pair.ConfigFile)
	default:
		// Read by the best effort, the config could not be read.
		return nil, failf("%w: %s describes no instances but %q, which stands for "+
			"those of the topology of %s, and that cannot be read: %w; nor is any "+
			"instance known on this host, with state under %s or %s",
			ErrNoInstances, pair.DescriptionFile(), instances.WildcardName,
			pair.ConfigFile, app.ConfigError,
			filepath.Join(app.Root, scope.RunDirName, app.Name),
			filepath.Join(app.Root, scope.DataDirName, app.Name))
	}
}

// selectInstances selects the records of app that names name, each an
// instance name or AllInstances.
func selectInstances(app *App, names []string) ([]instances.Instance, error) {
	wanted := make(map[string]bool, len(names))

	for _, name := range names {
		if name == AllInstances {
			return selectAll(app)
		}

		wanted[name] = true
	}

	selected := make([]instances.Instance, 0, len(wanted))

	for _, record := range app.Instances {
		if wanted[record.Name] {
			selected = append(selected, record)
			delete(wanted, record.Name)
		}
	}

	if len(wanted) > 0 {
		return nil, notDescribedError(app, names, wanted)
	}

	return selected, nil
}

// notDescribedError reports the names of missing, in the order names gives
// them, as instances the description of app does not declare.
func notDescribedError(app *App, names []string, missing map[string]bool) error {
	var quoted, running []string

	for _, name := range names {
		if missing[name] {
			quoted = append(quoted, fmt.Sprintf("%q", name))
			delete(missing, name)

			if app.wildcard && watchdogRuns(app, name) {
				running = append(running, fmt.Sprintf("%q", name))
			}
		}
	}

	known := make([]string, 0, len(app.Instances))
	for _, record := range app.Instances {
		known = append(known, record.Name)
	}

	described := "it describes none"
	if len(known) > 0 {
		described = "it describes " + strings.Join(known, ", ")
	}

	if len(running) > 0 {
		described += "; " + strings.Join(running, ", ") + " still runs, though the config " +
			"no longer has it: tt instance stop stops it, and it is not started again"
	}

	return failf("%w: %s of %q not in %s; %s", ErrInstanceNotFound,
		strings.Join(quoted, ", "), app.Name, app.Pair.DescriptionFile(), described)
}

// watchdogRuns reports whether the watchdog of the instance name of app runs
// in its scope, holding its pid file (runningInstances).
func watchdogRuns(app *App, name string) bool {
	if !validName(name) {
		return false
	}

	_, owned, err := pidfile.Owner(
		filepath.Join(app.Root, scope.RunDirName, app.Name, name, scope.PidFileName))

	return err == nil && owned
}
