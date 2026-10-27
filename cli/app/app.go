package app

import (
	"context"
	"fmt"
	"path/filepath"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/internal/instances"
)

// Options are what a command adds to its target to find the application.
type Options struct {
	// Scope is the --scope value; empty leaves the choice to the rules of
	// Load.
	Scope Scope
	// Template is the --template value; empty when it was not given, which
	// selects DefaultTemplate when the application runs from a template.
	Template string
	// Start is the directory the search for the project starts from: the
	// -C directory, or the working directory when it is empty.
	Start string
	// Dir pins the application directory: the application is loaded from
	// Dir, an absolute path, rather than looked for, and Scope must name
	// the scope Dir belongs to. Empty finds the application as usual.
	//
	// It is how a running instance gets back to its application, the
	// project or the very version it started from, while the pair is still
	// chosen and read anew. Dir applies to every application the targets of
	// Resolve name, so it goes with targets naming one.
	Dir string
	// Integrity checks the files the resolver reads and the config storage
	// the cluster config may point to. A context without a repository checks
	// nothing.
	Integrity integrity.IntegrityCtx
	// BestEffortConfig reads the cluster config of the pair, and the config
	// storage it names, as far as it can be read: a config that is missing,
	// broken or out of reach leaves App.Config nil and App.ConfigError set,
	// rather than failing the load, and the description is not checked
	// against the topology. The description is read and checked as always;
	// its "*" stands, without the config, for the instances that have run in
	// the scope, those with a directory in <root>/var/run/<app>/.
	//
	// It is for the commands that act on the processes of the instances,
	// which find them by the description and their pid files, such as
	// tt instance stop and status: an etcd that is down or a config broken
	// by an edit must not keep a running instance from being stopped or
	// looked at, as it did not in tt2; and a config that can be read still
	// says where the instance keeps what it moves out of the layout, such
	// as its console socket. A command that needs the config to act, such
	// as tt instance start, requires it.
	BestEffortConfig bool
}

// App is an application found and loaded with its pair.
type App struct {
	// Name is the name of the application: [package].name of the project's
	// manifest or the name of the project directory, and the directory name
	// under <root>/apps/ in the user and system scopes.
	Name string
	// Scope is the scope the application was found in.
	Scope Scope
	// Root is the root of that scope: .tarantool in the project directory,
	// or the user or the system root.
	Root string
	// Dir is the application directory: the project directory, or the
	// version directory <root>/apps/<app> pointed to when it was resolved.
	// It is the working directory of the Tarantool instances, and the
	// relative paths of the description are resolved against it.
	Dir string
	// Pair is the pair the application runs from.
	Pair Pair
	// Config is the cluster config of the pair, with what a config storage
	// it names holds merged in. It is nil when the description has no
	// Tarantool instance configured by it (HasClusterInstances): the config
	// is not read then, whether the pair has one or not. With
	// Options.BestEffortConfig it is nil as well when it, or the topology in
	// it, cannot be read.
	Config *goconfig.MutableConfig
	// BestEffortConfig is Options.BestEffortConfig the application was
	// loaded with: Config may lack an instance of the description, or hold
	// what does not fit it, and the instance is then taken as the layout
	// gives it.
	BestEffortConfig bool
	// ConfigError is why the config of the pair could not be read, with
	// Options.BestEffortConfig; nil when it was read or not needed.
	ConfigError error
	// Instances are every instance of the description, in its order: its
	// records, and the instances "*" stands for in place of it.
	Instances []instances.Instance

	// wildcard tells that the description has "*", or that the pair has
	// none, so that the application is one of the cluster config even when
	// "*" stands for no instance.
	wildcard bool
}

// HasClusterInstances reports whether the description of app has Tarantool
// instances configured by the cluster config, rather than only instances
// that run their scripts and external processes. "*" counts, whatever
// instances it stands for: it is the topology of the cluster config.
func (app *App) HasClusterInstances() bool {
	return app.wildcard || len(clusterInstances(app.Instances)) > 0
}

// Load finds the application name names, chooses its pair, and loads and
// checks the pair. An empty name is the project the command runs in; it is
// an error outside of a project and with --scope user or system.
//
// Load checks the pair as a whole, whichever instances a command then acts
// on: a Tarantool instance the topology does not declare, or a field an
// instance cannot honour, breaks every command on the application alike,
// but for those that read the config as far as they can
// (Options.BestEffortConfig), which do not check the topology.
func Load(ctx context.Context, name string, opts Options) (*App, error) {
	if name != "" && !validName(name) {
		return nil, failf("%w: %q cannot be an application name", ErrInvalidTarget, name)
	}

	start := opts.Start
	if start == "" {
		start = "."
	}

	start, err := filepath.Abs(start)
	if err != nil {
		return nil, failf("cannot resolve the start directory %q: %w", opts.Start, err)
	}

	//nolint:contextcheck // The scope roots ask uname and take no context.
	loc, err := locate(name, opts, start)
	if err != nil {
		return nil, err
	}

	pair, err := selectPair(loc, opts.Template)
	if err != nil {
		return nil, err
	}

	loaded, err := loadPair(ctx, pair, loc, opts.Integrity, opts.BestEffortConfig)
	if err != nil {
		return nil, fmt.Errorf("application %q: %w", loc.name, err)
	}

	configErr := loaded.configErr
	if configErr != nil {
		configErr = fmt.Errorf("application %q: %w", loc.name, configErr)
	}

	return &App{
		Name:             loc.name,
		Scope:            loc.scope,
		Root:             loc.root,
		Dir:              loc.dir,
		Pair:             pair,
		Config:           loaded.config,
		BestEffortConfig: opts.BestEffortConfig,
		ConfigError:      configErr,
		Instances:        loaded.records,
		wildcard:         loaded.wildcard,
	}, nil
}
