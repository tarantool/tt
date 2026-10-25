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
	// Integrity checks the files the resolver reads and the config storage
	// the cluster config may point to. A context without a repository checks
	// nothing.
	Integrity integrity.IntegrityCtx
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
	// it names holds merged in.
	Config *goconfig.MutableConfig
	// Instances are every record of the description, in its order.
	Instances []instances.Instance
}

// Load finds the application name names, chooses its pair, and loads and
// checks the pair. An empty name is the project the command runs in; it is
// an error outside of a project and with --scope user or system.
//
// Load checks the pair as a whole, whichever instances a command then acts
// on: a Tarantool instance the topology does not declare, or a field an
// instance cannot honour, breaks every command on the application alike.
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

	records, cfg, err := loadPair(ctx, pair, loc.dir, opts.Integrity)
	if err != nil {
		return nil, fmt.Errorf("application %q: %w", loc.name, err)
	}

	return &App{
		Name:      loc.name,
		Scope:     loc.scope,
		Root:      loc.root,
		Dir:       loc.dir,
		Pair:      pair,
		Config:    cfg,
		Instances: records,
	}, nil
}
