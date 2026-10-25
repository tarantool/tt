package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/internal/instances"
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
// app:* and no target select every record of the description, which is what
// runs on this host, not the whole topology of the cluster; selecting none
// is an error, since a command that does nothing while reporting success
// hides a mistake. app:inst must name a record of the description: an
// instance only the topology knows runs on another host. A target given
// twice selects its instance once.
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

// selectAll selects every record of the description of app.
func selectAll(app *App) ([]instances.Instance, error) {
	if len(app.Instances) == 0 {
		return nil, failf("%w: %s describes no instances", ErrNoInstances,
			app.Pair.InstancesFile)
	}

	return slices.Clone(app.Instances), nil
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
	var quoted []string

	for _, name := range names {
		if missing[name] {
			quoted = append(quoted, fmt.Sprintf("%q", name))
			delete(missing, name)
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

	return failf("%w: %s of %q not in %s; %s", ErrInstanceNotFound,
		strings.Join(quoted, ", "), app.Name, app.Pair.InstancesFile, described)
}
