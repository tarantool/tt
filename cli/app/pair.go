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

	// hostDirName is the directory under a user or system root holding the
	// pair of the host for every application, <root>/etc/<app>/.
	hostDirName = "etc"

	// implicitDescription is the description of a pair that has none: the
	// config alone runs every instance of its topology.
	implicitDescription = `"*": {}`
)

// Pair is the instances description and the cluster config an application
// runs from. The two agree on the names of the Tarantool instances, and
// different templates describe different topologies, so a pair is taken
// whole from one directory.
//
// The cluster config is needed only by the Tarantool instances it
// configures: a description of instances that run their scripts and of
// external processes alone makes a pair without one. A template of the
// config alone runs every instance of its topology, as the description
// "*": {} would; the pair of a host needs its description, which says what
// runs on that host.
type Pair struct {
	// Template is the name of the template the pair is, or empty for the
	// pair of the host, <root>/etc/<app>/.
	Template string
	// Dir is the directory holding the pair.
	Dir string
	// InstancesFile is the path of the instances description, empty for a
	// template without one.
	InstancesFile string
	// ConfigFile is the path of the cluster config, the file tarantool -c
	// gets; empty when the pair has none.
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

// DescriptionFile names, for a message, the file the instances of the pair
// are described by: the instances description, or the cluster config of a
// template without one, whose topology is all the description it has.
func (pair *Pair) DescriptionFile() string {
	if pair.InstancesFile == "" {
		return pair.ConfigFile
	}

	return pair.InstancesFile
}

// HostDir returns the directory of the host files of the application name
// installed under root, the root of the user or the system scope:
// <root>/etc/<app>/. It holds the pair of the host, and the files tt keeps
// for the application on the host, such as the mark of its systemd units.
func HostDir(root, name string) string {
	return filepath.Join(root, hostDirName, name)
}

// newPair returns the pair in dir.
func newPair(template, dir string) Pair {
	return Pair{
		Template:      template,
		Dir:           dir,
		InstancesFile: filepath.Join(dir, scope.InstancesFileName),
		ConfigFile:    filepath.Join(dir, scope.ConfigFileName),
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
// it the pair: a config without a description, or a description that needs
// the config it lacks (loadPair), is an incomplete pair and an error, not a
// reason to run the template, which would start a topology the host was not
// given.
//
// Unlike a template, the pair of a host does not take a config alone for
// "*": {}. A host of a cluster spread over several runs a part of its
// topology, and a description forgotten on one of them would start there
// the instances that already run on the others.
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
		host, present, err := hostPair(loc)
		if err != nil {
			return Pair{}, err
		}

		if present && template != "" {
			return Pair{}, failf("%w: %q runs from %s; remove --template",
				ErrTemplateConflict, loc.name, host.String())
		}

		if present {
			return host, nil
		}
	}

	if template == "" {
		template = DefaultTemplate
	}

	pair := newPair(template, filepath.Join(loc.dir, scope.TemplatesDirName, template))

	present, err := checkPair(&pair)
	if err != nil {
		return Pair{}, err
	}

	if !present {
		return Pair{}, noTemplateError(loc, pair)
	}

	return pair, nil
}

// hostPair returns the pair of the host of the application at loc, an
// application of the user or the system scope, and whether the host has one.
// A host with a config and no description has an incomplete pair (see
// selectPair).
func hostPair(loc location) (Pair, bool, error) {
	host := newPair("", HostDir(loc.root, loc.name))

	present, err := checkPair(&host)
	if err != nil {
		return Pair{}, false, err
	}

	if present && host.InstancesFile == "" {
		return Pair{}, false, failf("%w: %s has %s but no %s next to it; the pair of a "+
			"host describes what runs on the host, %q: {} for every instance of the "+
			"topology", ErrIncompletePair, host.String(), scope.ConfigFileName,
			scope.InstancesFileName, instances.WildcardName)
	}

	return host, present, nil
}

// validTemplate checks a --template value: it names one directory under
// .templates/.
func validTemplate(name string) error {
	if !validName(name) {
		return failf("%w %q", ErrInvalidTemplate, name)
	}

	return nil
}

// checkPair reports whether pair is there: true when either of its files is,
// false when neither is. A file the pair lacks gets an empty path: whether
// the description can do without a config is up to loadPair, which reads
// them, and whether the pair can do without a description, to selectPair.
func checkPair(pair *Pair) (bool, error) {
	hasInstances, err := exists(pair.InstancesFile)
	if err != nil {
		return false, failf("%w", err)
	}

	hasConfig, err := exists(pair.ConfigFile)
	if err != nil {
		return false, failf("%w", err)
	}

	if !hasInstances {
		pair.InstancesFile = ""
	}

	if !hasConfig {
		pair.ConfigFile = ""
	}

	return hasInstances || hasConfig, nil
}

// noTemplateError reports that the application at loc has no pair, naming
// the templates it does have.
func noTemplateError(loc location, pair Pair) error {
	templates := listTemplates(filepath.Join(loc.dir, scope.TemplatesDirName))
	if len(templates) == 0 {
		return failf("%w: %q has no %s or %s in %s", ErrNoPair, loc.name,
			scope.InstancesFileName, scope.ConfigFileName, pair.Dir)
	}

	return failf("%w: %q has no template %q in %s; its templates are %s, "+
		"pick one with --template", ErrNoPair, loc.name, pair.Template,
		filepath.Dir(pair.Dir), strings.Join(templates, ", "))
}

// listTemplates returns the names of the templates in templatesDir, the
// directories that hold either file of a pair, sorted. A failure to look
// gives no names: this only adds a hint to an error.
func listTemplates(templatesDir string) []string {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		return nil
	}

	var names []string

	for _, entry := range entries {
		pair := newPair(entry.Name(), filepath.Join(templatesDir, entry.Name()))

		found, err := checkPair(&pair)
		if found && err == nil {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names
}

// loadedPair is what loadPair reads from a pair.
type loadedPair struct {
	// records are the instances of the description, with "*" replaced by
	// the instances it stands for.
	records []instances.Instance
	// wildcard tells that the description has "*", or that the pair has
	// none and runs the whole topology of its config.
	wildcard bool
	// config is the cluster config, nil when it is not read.
	config *goconfig.MutableConfig
	// configErr is why the config could not be read, when it was read as far
	// as it can be.
	configErr error
}

// loadPair reads the instances description and the cluster config of pair,
// for the application at loc, and checks that they fit together.
//
// The description is parsed and checked before the config is loaded: it is a
// local file, while the config may send tt to etcd for the rest of the
// cluster config, and a typo in the description is better reported without
// a round trip.
//
// The config is read only for the Tarantool instances it configures, and for
// "*", which stands for those of its topology that the description does not
// name (expand). A description without them, of instances that run their
// scripts and of external processes, needs no config, and one the pair has
// anyway is left unread: whatever it holds, an empty file or a storage out of
// reach, does not keep those instances from running. A description with them
// needs the config, and a pair without one is incomplete.
//
// With Options.BestEffortConfig the config is read as far as it can be: why
// it cannot is configErr rather than a failure, and the topology is not
// checked. "*" then stands for the instances that have run in the scope of
// the application (ranInstances): a config storage that is down must not
// keep them from being stopped. With Options.SkipTopologyCheck the config is
// required and the topology is not checked either.
func loadPair(ctx context.Context, pair Pair, loc location, opts Options) (loadedPair, error) {
	bestEffort := opts.BestEffortConfig
	check := !bestEffort && !opts.SkipTopologyCheck

	desc, err := readDescription(pair, loc.dir, opts.Integrity)
	if err != nil {
		return loadedPair{}, err
	}

	err = checkFields(pair, desc)
	if err != nil {
		return loadedPair{}, err
	}

	loaded := loadedPair{
		records:   desc.Instances,
		wildcard:  desc.Wildcard != nil,
		config:    nil,
		configErr: nil,
	}

	if len(clusterRecords(desc)) == 0 {
		return loaded, nil
	}

	cfg, topology, err := readConfig(ctx, pair, desc, opts.Integrity,
		check || desc.Wildcard != nil)
	if err != nil && !bestEffort {
		return loadedPair{}, err
	}

	if err != nil {
		loaded.configErr = err

		if desc.Wildcard != nil {
			ran, listErr := ranInstances(loc)
			if listErr != nil {
				return loadedPair{}, listErr
			}

			loaded.records = expand(desc, ran)
		}

		return loaded, nil
	}

	loaded.config = cfg
	loaded.records = expand(desc, topology)

	if !check {
		return loaded, nil
	}

	err = checkTopology(pair.InstancesFile, pair.ConfigFile, desc.Instances, topology)
	if err != nil {
		return loadedPair{}, err
	}

	return loaded, nil
}

// readDescription reads and parses the instances description of pair, for
// the application in appDir. A pair without one runs every instance of the
// topology of its config, and gets the description that says so.
func readDescription(pair Pair, appDir string, integ integrity.IntegrityCtx) (
	instances.Description, error,
) {
	data := []byte(implicitDescription)

	if pair.InstancesFile != "" {
		var err error

		data, err = readFile(integ, pair.InstancesFile)
		if err != nil {
			return instances.Description{}, failf("%w", err)
		}
	}

	desc, err := instances.Parse(data, appDir)
	if err != nil {
		return instances.Description{}, failf("invalid instance description %s:\n%w",
			pair.DescriptionFile(), err)
	}

	return desc, nil
}

// readConfig reads the cluster config of pair, which some records of desc
// need (clusterRecords), and, with withTopology, the topology of the config,
// which "*" stands for and the description is checked against.
func readConfig(
	ctx context.Context,
	pair Pair,
	desc instances.Description,
	integ integrity.IntegrityCtx,
	withTopology bool,
) (*goconfig.MutableConfig, []string, error) {
	if pair.ConfigFile == "" {
		return nil, nil, failf("%w: %s has %s but no %s next to it to configure "+
			"its Tarantool instances %s", ErrIncompletePair, pair.String(),
			scope.InstancesFileName, scope.ConfigFileName, quoteNames(clusterRecords(desc)))
	}

	cfg, err := cluster.GetClusterConfig(ctx, pair.ConfigFile, integ)
	if err != nil {
		return nil, nil, failf("%w", err)
	}

	if !withTopology {
		return cfg, nil, nil
	}

	topology, err := cluster.Instances(cfg.Snapshot())
	if err != nil {
		return nil, nil, failf("cannot read the topology of %s: %w", pair.ConfigFile, err)
	}

	return cfg, topology, nil
}

// expand returns the instances of desc: its records, with "*" replaced by the
// instances of topology that no record names, in the order of their names,
// where "*" is written. A record naming an instance of the topology replaces
// "*" for it whole: none of the fields of "*" go into it.
func expand(desc instances.Description, topology []string) []instances.Instance {
	if desc.Wildcard == nil {
		return desc.Instances
	}

	named := make(map[string]bool, len(desc.Instances))
	for _, record := range desc.Instances {
		named[record.Name] = true
	}

	rest := slices.Compact(slices.Sorted(slices.Values(topology)))

	records := make([]instances.Instance, 0, len(desc.Instances)+len(rest))

	records = append(records, desc.Instances[:desc.Wildcard.At]...)

	for _, name := range rest {
		if !named[name] {
			records = append(records, desc.Wildcard.Instance(name))
		}
	}

	return append(records, desc.Instances[desc.Wildcard.At:]...)
}

// ranInstances returns the names of the instances of the application at loc
// that have a run directory under the root of its scope,
// <root>/var/run/<app>/<inst>/, sorted: those that run or have run there,
// since the directory outlives the instance. Without the config of the pair
// these are what "*" stands for to a command that acts on the processes of
// the instances. A root without the directory has none; one that cannot be
// read is an error, so that a command does not report success while it
// leaves some of the instances meant out.
func ranInstances(loc location) ([]string, error) {
	dir := filepath.Join(loc.root, scope.RunDirName, loc.name)

	entries, err := os.ReadDir(dir)
	if isAbsent(err) {
		return nil, nil
	}

	if err != nil {
		return nil, failf("cannot look for the instances that have run: %w", err)
	}

	var names []string

	for _, entry := range entries {
		if entry.IsDir() && validName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}

	return names, nil
}

// checkFields refuses the fields of a record that an instance of its kind
// cannot honour, "*" among the records.
//
// A Tarantool instance, configured by the cluster config or by its script,
// runs in the application directory, so that relative paths in its config
// mean the same as everywhere else in the application; and tt makes up its
// command line. workdir and args in its record would be silently ignored,
// which is the tt2 behaviour users complained about, so they are refused. So
// is logrotate_signal: tarantool reopens its log on SIGHUP, which
// tt instance logrotate always sends it. Allowing them later breaks no
// description.
func checkFields(pair Pair, desc instances.Description) error {
	const (
		runs      = "a Tarantool instance runs in the application directory"
		arguments = "a Tarantool instance runs with the arguments tt gives it"
		logrotate = "a Tarantool instance reopens its log on SIGHUP, " +
			"which tt instance logrotate sends it"
	)

	records := desc.Instances
	if desc.Wildcard != nil {
		records = slices.Insert(slices.Clone(records), desc.Wildcard.At, desc.Wildcard.Fields)
	}

	var errs []error

	for _, record := range records {
		if record.Kind() == instances.KindExternal {
			continue
		}

		if record.Workdir != "" {
			errs = append(errs, fieldError(record.Name, "workdir", runs))
		}

		if len(record.Args) > 0 {
			errs = append(errs, fieldError(record.Name, "args", arguments))
		}

		if record.LogrotateSignal != 0 {
			errs = append(errs, fieldError(record.Name, "logrotate_signal", logrotate))
		}
	}

	if len(errs) > 0 {
		return failf("%s:\n%w", pair.DescriptionFile(), errors.Join(errs...))
	}

	return nil
}

// fieldError reports a field a Tarantool instance cannot have, and why. inst
// is the name of its record, "*" included.
func fieldError(inst, field, why string) error {
	record := "instance"
	if inst == instances.WildcardName {
		record = "record"
	}

	return fmt.Errorf("%s %q: %w %s: %s; %s is only for an external process (binary)",
		record, inst, ErrUnsupportedField, field, why, field)
}

// checkTopology checks records, of the description descFile, against
// topology, the instances of the cluster config named config: every
// Tarantool instance the cluster config configures is in it, and no
// instance that runs its script and no external process takes a name of it.
//
// The topology may hold more: the description says what runs on this host,
// and the rest of the cluster runs on others, unless "*" takes it here. A
// record with a script or a binary is not in the topology, as the cluster
// config does not configure it; one that takes a name of the topology would
// silently take the place of the instance of the cluster, "*" standing for
// it or not, and both would answer to the name.
func checkTopology(descFile, config string, records []instances.Instance,
	topology []string,
) error {
	var missing, taken []string

	for _, record := range records {
		inTopology := slices.Contains(topology, record.Name)

		switch {
		case record.Kind() == instances.KindCluster && !inTopology:
			missing = append(missing, record.Name)
		case record.Kind() != instances.KindCluster && inTopology:
			taken = append(taken, record.Name)
		}
	}

	var errs []error

	if len(missing) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s from %s not declared in %s",
			ErrTopologyMismatch, quoteNames(missing), descFile, config))
	}

	if len(taken) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s of %s, with script or binary, take the "+
			"names of instances of the cluster declared in %s; a record of an instance "+
			"of the cluster has neither", ErrTopologyConflict, quoteNames(taken),
			descFile, config))
	}

	if len(errs) > 0 {
		return failf("%w", errors.Join(errs...))
	}

	return nil
}

// CheckTopology checks the instances of app against topology, the instances
// of a cluster config about to take the place of the config of its pair, as
// tt cluster publish publishes one; config names that config for a message.
// Every Tarantool instance of the cluster config app runs, those "*" stands
// for now among them, must stay in it, and no instance that runs its script
// and no external process may take a name of it. A config that leaves an
// instance of the description out would leave the pair broken, or an
// instance of "*" running without its config.
func (app *App) CheckTopology(topology []string, config string) error {
	err := checkTopology(app.Pair.DescriptionFile(), config, app.Instances, topology)
	if err != nil && app.wildcard && errors.Is(err, ErrTopologyMismatch) {
		return failf("%w;\n%q stands for the instances of the topology the pair has "+
			"now: a config that drops some of them is to be written by hand, or "+
			"published to its storage by the URI", err, instances.WildcardName)
	}

	return err
}

// CheckTemplate checks a template the way Load checks a pair, for
// tt package pack: desc, the description descFile, against the topology of
// the cluster config file configFile, when that file holds the cluster
// config itself. A config that points to a config storage (config.etcd,
// config.storage) is left unread, and checked reports so: the storage may be
// out of reach where the package is packed, and the instances check it when
// they start. The file alone is read, without the TT_ environment of the
// packing host, since the template goes into the package as it is.
func CheckTemplate(ctx context.Context, desc instances.Description, descFile, configFile string,
) (bool, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return false, failf("%w", err)
	}

	cfg, err := cluster.BuildGoConfigFromBytes(ctx, data)
	if err != nil {
		return false, failf("cannot read %s: %w", configFile, err)
	}

	_, stored := cluster.StorageReference(cfg)
	if stored {
		return false, nil
	}

	topology, err := cluster.Instances(cfg)
	if err != nil {
		return false, failf("cannot read the topology of %s: %w", configFile, err)
	}

	return true, checkTopology(descFile, configFile, desc.Instances, topology)
}

// clusterInstances returns the names of the records that are Tarantool
// instances configured by the cluster config, in their order.
func clusterInstances(records []instances.Instance) []string {
	var names []string

	for _, record := range records {
		if record.Kind() == instances.KindCluster {
			names = append(names, record.Name)
		}
	}

	return names
}

// clusterRecords returns the names of the records of desc that need the
// cluster config, in the order of the description: the Tarantool instances
// it configures, and "*", which stands for its topology.
func clusterRecords(desc instances.Description) []string {
	names := clusterInstances(desc.Instances)
	if desc.Wildcard == nil {
		return names
	}

	// "*" goes before the first of the records written after it.
	at := len(clusterInstances(desc.Instances[:desc.Wildcard.At]))

	return slices.Insert(names, at, instances.WildcardName)
}

// quoteNames lists names for a message, each quoted.
func quoteNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}

	return strings.Join(quoted, ", ")
}
