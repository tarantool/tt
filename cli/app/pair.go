package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/scope"
)

const (
	// DefaultTemplate is the template a command runs without --template.
	DefaultTemplate = "default"

	// configFileName is the cluster config of a pair, the file tarantool -c
	// gets.
	configFileName = "config.yaml"
	// hostDirName is the directory under a user or system root holding the
	// pair of the host for every application, <root>/etc/<app>/.
	hostDirName = "etc"
)

// Pair is the instances description and the cluster config an application
// runs from. The two agree on the names of the Tarantool instances, and
// different templates describe different topologies, so a pair is taken
// whole from one directory.
type Pair struct {
	// Template is the name of the template the pair is, or empty for the
	// pair of the host, <root>/etc/<app>/.
	Template string
	// Dir is the directory holding the pair.
	Dir string
	// InstancesFile is the path of the instances description.
	InstancesFile string
	// ConfigFile is the path of the cluster config.
	ConfigFile string
}

// Host reports whether the pair is the pair of the host rather than a
// template.
func (pair *Pair) Host() bool {
	return pair.Template == ""
}

// String names the pair for a message.
func (pair *Pair) String() string {
	if pair.Host() {
		return "the pair of the host in " + pair.Dir
	}

	return fmt.Sprintf("template %q in %s", pair.Template, pair.Dir)
}

// newPair returns the pair in dir.
func newPair(template, dir string) Pair {
	return Pair{
		Template:      template,
		Dir:           dir,
		InstancesFile: filepath.Join(dir, scope.InstancesFileName),
		ConfigFile:    filepath.Join(dir, configFileName),
	}
}

// selectPair chooses the pair of the application at loc: in the user and
// system scopes the pair of the host when it has one, and the template
// otherwise; in a project, always the template. template is the --template
// value, empty when it was not given.
//
// The pair of the host lives outside of the version, because a version is
// immutable and the description of what runs on a host must survive
// switching to the next one; that is why it wins. Either of its files makes
// it the pair: one without the other is an incomplete pair and an error, not
// a reason to run the template, which would start a topology the host was
// not given.
//
// --template is refused when the host has a pair. The host chose what runs
// on it; a template that quietly loses to the host pair would run something
// other than what was asked for, and one that wins would bypass the host.
func selectPair(loc location, template string) (Pair, error) {
	if template != "" {
		err := validTemplate(template)
		if err != nil {
			return Pair{}, err
		}
	}

	if loc.scope != ScopeProject {
		host := newPair("", filepath.Join(loc.root, hostDirName, loc.name))

		present, err := checkPair(host)
		if err != nil {
			return Pair{}, err
		}

		if present {
			if template != "" {
				return Pair{}, failf("%w: %q runs from %s; remove --template",
					ErrTemplateConflict, loc.name, host.String())
			}

			return host, nil
		}
	}

	if template == "" {
		template = DefaultTemplate
	}

	pair := newPair(template, filepath.Join(loc.dir, scope.TemplatesDirName, template))

	present, err := checkPair(pair)
	if err != nil {
		return Pair{}, err
	}

	if !present {
		return Pair{}, noTemplateError(loc, pair)
	}

	return pair, nil
}

// validTemplate checks a --template value: it names one directory under
// .templates/.
func validTemplate(name string) error {
	if !validName(name) {
		return failf("%w %q", ErrInvalidTemplate, name)
	}

	return nil
}

// checkPair reports whether the files of pair are there: false when neither
// is, true when both are, and an error when only one is.
func checkPair(pair Pair) (bool, error) {
	hasInstances, err := exists(pair.InstancesFile)
	if err != nil {
		return false, failf("%w", err)
	}

	hasConfig, err := exists(pair.ConfigFile)
	if err != nil {
		return false, failf("%w", err)
	}

	switch {
	case hasInstances && hasConfig:
		return true, nil
	case hasInstances:
		return false, failf("%w: %s has %s but no %s next to it", ErrIncompletePair,
			pair.String(), scope.InstancesFileName, configFileName)
	case hasConfig:
		return false, failf("%w: %s has %s but no %s next to it", ErrIncompletePair,
			pair.String(), configFileName, scope.InstancesFileName)
	default:
		return false, nil
	}
}

// noTemplateError reports that the application at loc has no pair, naming
// the templates it does have.
func noTemplateError(loc location, pair Pair) error {
	templates := listTemplates(filepath.Join(loc.dir, scope.TemplatesDirName))
	if len(templates) == 0 {
		return failf("%w: %q has no %s in %s", ErrNoPair, loc.name,
			scope.InstancesFileName, pair.Dir)
	}

	return failf("%w: %q has no template %q in %s; its templates are %s, "+
		"pick one with --template", ErrNoPair, loc.name, pair.Template,
		filepath.Dir(pair.Dir), strings.Join(templates, ", "))
}

// listTemplates returns the names of the templates in templatesDir that hold
// an instances description, sorted. A failure to look gives no names: this
// only adds a hint to an error.
func listTemplates(templatesDir string) []string {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		return nil
	}

	var names []string

	for _, entry := range entries {
		found, err := exists(filepath.Join(templatesDir, entry.Name(), scope.InstancesFileName))
		if found && err == nil {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names
}

// loadPair reads the instances description and the cluster config of pair,
// for the application in appDir, and checks that they fit together.
//
// The description is parsed and checked before the config is loaded: it is a
// local file, while the config may send tt to etcd for the rest of the
// cluster config, and a typo in the description is better reported without
// a round trip.
func loadPair(
	ctx context.Context,
	pair Pair,
	appDir string,
	integ integrity.IntegrityCtx,
) ([]instances.Instance, *goconfig.MutableConfig, error) {
	data, err := readFile(integ, pair.InstancesFile)
	if err != nil {
		return nil, nil, failf("%w", err)
	}

	records, err := instances.Parse(data, appDir)
	if err != nil {
		return nil, nil, failf("invalid instance description %s:\n%w", pair.InstancesFile, err)
	}

	err = checkFields(pair, records)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := cluster.GetClusterConfig(ctx, pair.ConfigFile, integ)
	if err != nil {
		return nil, nil, failf("%w", err)
	}

	err = checkTopology(pair, records, cfg)
	if err != nil {
		return nil, nil, err
	}

	return records, cfg, nil
}

// checkFields refuses the fields of a record that an instance of its kind
// cannot honour.
//
// A Tarantool instance, configured by the cluster config or by its script,
// runs in the application directory, so that relative paths in its config
// mean the same as everywhere else in the application; and tt makes up its
// command line. workdir and args in its record would be silently ignored,
// which is the tt2 behaviour users complained about, so they are refused.
// Allowing them later breaks no description.
func checkFields(pair Pair, records []instances.Instance) error {
	var errs []error

	for _, record := range records {
		if record.Kind() == instances.KindExternal {
			continue
		}

		if record.Workdir != "" {
			errs = append(errs, fieldError(record.Name, "workdir"))
		}

		if len(record.Args) > 0 {
			errs = append(errs, fieldError(record.Name, "args"))
		}
	}

	if len(errs) > 0 {
		return failf("%s:\n%w", pair.InstancesFile, errors.Join(errs...))
	}

	return nil
}

// fieldError reports a field a Tarantool instance cannot have.
func fieldError(inst, field string) error {
	return fmt.Errorf("instance %q: %w %s: a Tarantool instance runs in the "+
		"application directory with the arguments tt gives it; "+
		"%s is only for an external process (binary)", inst, ErrUnsupportedField, field, field)
}

// checkTopology checks that every Tarantool instance the description
// configures through the cluster config is in its topology.
//
// The topology may hold more: the description says what runs on this host,
// and the rest of the cluster runs on others. Instances with a script or a
// binary are not checked, since the cluster config does not configure them.
func checkTopology(pair Pair, records []instances.Instance, cfg *goconfig.MutableConfig) error {
	var declared []string

	for _, record := range records {
		if record.Kind() == instances.KindCluster {
			declared = append(declared, record.Name)
		}
	}

	if len(declared) == 0 {
		return nil
	}

	topology, err := cluster.Instances(cfg.Snapshot())
	if err != nil {
		return failf("cannot read the topology of %s: %w", pair.ConfigFile, err)
	}

	var missing []string

	for _, name := range declared {
		if !slices.Contains(topology, name) {
			missing = append(missing, fmt.Sprintf("%q", name))
		}
	}

	if len(missing) > 0 {
		return failf("%w: %s from %s not declared in %s", ErrTopologyMismatch,
			strings.Join(missing, ", "), pair.InstancesFile, pair.ConfigFile)
	}

	return nil
}
