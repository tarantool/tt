package sdk

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
)

// instanceDelimiter separates an application from an instance in a
// reference to an instance, as in "app:instance".
const instanceDelimiter = ":"

// Credentials are a user name and a password for a configuration storage.
type Credentials struct {
	// Username is the user name; empty when none is given.
	Username string
	// Password is the password; empty when none is given.
	Password string
}

// ClusterConfig is a cluster configuration and the directory it was read
// from, as [Services.ClusterConfig] returns them.
type ClusterConfig struct {
	// Config is the configuration: an immutable go-config snapshot carrying
	// the Tarantool hierarchy, so [Instances] and [InstanceConfig] read it.
	Config goconfig.Config
	// Dir is the absolute directory a relative path in the configuration -
	// such as an SSL file of an instance - is relative to: the application
	// directory, where its instances run, for an application, and the
	// directory of the file for a cluster configuration file. It is "" for a
	// configuration storage, whose paths have no directory of their own.
	Dir string
}

// sourceKind is the kind of place a ClusterSource names.
type sourceKind uint8

const (
	// sourceNone is the zero ClusterSource, which names nothing.
	sourceNone sourceKind = iota
	// sourceApp is an application, as the instance commands find it.
	sourceApp
	// sourceFile is a cluster configuration file.
	sourceFile
	// sourceStorage is an etcd or a Tarantool config storage.
	sourceStorage
)

// ClusterSource names where a cluster configuration comes from: an
// application ([AppSource]), a cluster configuration
// file ([FileSource]) or a configuration storage ([StorageSource]).
// [ParseClusterSource] tells them apart in a command-line argument.
//
// The zero ClusterSource names nothing, and [Services.ClusterConfig] refuses
// it. ClusterSource is comparable: two sources are equal when they were built
// by the same constructor from the same arguments, with the same
// [AppLookup], so a source can be a map key.
type ClusterSource struct {
	kind   sourceKind
	name   string
	creds  Credentials
	lookup AppLookup
}

// The flags with which a command of tt that takes an application finds it,
// as the instance commands do. [AddAppFlags] declares them.
const (
	// ScopeFlag is --scope, the scope the application is looked for in:
	// project, user or system.
	ScopeFlag = "scope"
	// TemplateFlag is --template, the template of the application its
	// instances and its cluster config are taken from.
	TemplateFlag = "template"
)

// defaultTemplate is the template of an application without --template.
const defaultTemplate = "default"

// AppLookup is how the application of an [AppSource] is found: the scope it
// is looked for in and the template its pair, the instances description and
// the cluster config, is taken from - the values of --scope and --template
// ([AddAppFlags]). An empty field leaves the choice to the rules the
// instance commands follow without the flag: the project the command runs in
// when it has the name, and the system scope otherwise; the template the
// application runs from, "default" unless the host has a pair of its own.
type AppLookup struct {
	// Scope is "project", "user" or "system", or empty.
	Scope string
	// Template is the name of a template of the application, or empty.
	Template string
}

// AddAppFlags declares --scope and --template on cmd, bound to lookup, as
// every command of tt that takes an application declares them, with the
// same help and the same completion. The values are checked when the
// application is looked for: [Services.ClusterConfig] refuses a scope other
// than project, user and system, and a template the application cannot
// have.
func AddAppFlags(cmd *cobra.Command, lookup *AppLookup) {
	flags := cmd.Flags()

	flags.StringVar(&lookup.Scope, ScopeFlag, "",
		"scope the application is looked for in: project, user or system")
	flags.StringVar(&lookup.Template, TemplateFlag, "",
		`template of the application the instances are described in (default "`+
			defaultTemplate+`")`)

	_ = cmd.RegisterFlagCompletionFunc(ScopeFlag, cobra.FixedCompletions(
		[]string{"project", "user", "system"}, cobra.ShellCompDirectiveNoFileComp))
}

// AppSource names the cluster configuration of the application name, found
// as the instance commands find it without --scope and --template: the
// cluster config of the pair it runs from, together with the TT_*
// environment and the centralized storage the file names. name is an
// application name without an instance; see [SplitInstance].
// [ClusterSource.WithLookup] finds it as the flags say.
func AppSource(name string) ClusterSource {
	return ClusterSource{
		kind: sourceApp, name: name, creds: Credentials{Username: "", Password: ""},
		lookup: AppLookup{Scope: "", Template: ""},
	}
}

// FileSource names the cluster configuration file at path, together with
// the TT_* environment and the centralized storage the file names. A
// relative path is relative to the working directory.
func FileSource(path string) ClusterSource {
	return ClusterSource{
		kind: sourceFile, name: path, creds: Credentials{Username: "", Password: ""},
		lookup: AppLookup{Scope: "", Template: ""},
	}
}

// StorageSource names the cluster configuration kept in an etcd or a
// Tarantool config storage under the prefix of uri, such as
// "http://user:pass@localhost:2379/prefix?timeout=5". The storage alone is
// read: no file and no environment are merged in.
//
// The credentials used are the ones uri carries when it carries a user name
// or a password. Otherwise each of creds' fields is used, and an empty one
// is taken from the environment: TT_CLI_ETCD_USERNAME and
// TT_CLI_ETCD_PASSWORD for etcd, TT_CLI_USERNAME and TT_CLI_PASSWORD for a
// Tarantool config storage.
func StorageSource(uri string, creds Credentials) ClusterSource {
	return ClusterSource{
		kind: sourceStorage, name: uri, creds: creds,
		lookup: AppLookup{Scope: "", Template: ""},
	}
}

// WithLookup returns s, built by [AppSource], with its application found as
// lookup says, as --scope and --template of a command give it. Any other
// source is returned as it is: a file and a storage are not looked for.
func (s ClusterSource) WithLookup(lookup AppLookup) ClusterSource {
	if s.kind == sourceApp {
		s.lookup = lookup
	}

	return s
}

// Lookup returns how the application of a source built by [AppSource] is
// found ([ClusterSource.WithLookup]), and the zero AppLookup for any other
// source.
func (s ClusterSource) Lookup() AppLookup {
	return s.lookup
}

// ParseClusterSource classifies arg, a command-line argument naming a cluster
// configuration, the way tt's own commands do: a storage URI (a URL with a
// scheme and a host, see [github.com/tarantool/tt/sdk/connect.CreateURIOpts])
// is a [StorageSource]; otherwise the path of an existing regular file is a
// [FileSource]; anything else is an [AppSource]. creds are used by a storage
// source only.
//
// An argument that names an instance too, such as "app:instance", is split
// with [SplitInstance] first.
func ParseClusterSource(arg string, creds Credentials) ClusterSource {
	_, err := connect.CreateURIOpts(arg)
	if err == nil {
		return StorageSource(arg, creds)
	}

	info, err := os.Stat(arg)
	if err == nil && info.Mode().IsRegular() {
		return FileSource(arg)
	}

	return AppSource(arg)
}

// App returns the application name of a source built by [AppSource], and
// whether it was.
func (s ClusterSource) App() (string, bool) {
	return s.name, s.kind == sourceApp
}

// File returns the path of a source built by [FileSource], and whether it
// was.
func (s ClusterSource) File() (string, bool) {
	return s.name, s.kind == sourceFile
}

// Storage returns the URI and the credentials of a source built by
// [StorageSource], and whether it was.
func (s ClusterSource) Storage() (string, Credentials, bool) {
	return s.name, s.creds, s.kind == sourceStorage
}

// String describes the source for a message: `application "name"`,
// `file "path"` or `storage "uri"`. The URI is shown without its user
// information and the credentials are never shown.
func (s ClusterSource) String() string {
	switch s.kind {
	case sourceApp:
		return fmt.Sprintf("application %q", s.name)
	case sourceFile:
		return fmt.Sprintf("file %q", s.name)
	case sourceStorage:
		return fmt.Sprintf("storage %q", log.RedactURL(s.name))
	case sourceNone:
		return "no source"
	default:
		return "unknown source"
	}
}

// SplitInstance splits ref, a reference to an instance of the form
// "app:instance", into the application and the instance. A ref without the
// delimiter is an application alone, and instance is empty. Only the first
// delimiter splits: "a:b:c" is the instance "b:c" of the application "a".
func SplitInstance(ref string) (string, string) {
	app, instance, _ := strings.Cut(ref, instanceDelimiter)

	return app, instance
}

// instancePath is the layout of the path to an instance in a cluster
// configuration: groups/<group>/replicasets/<replicaset>/instances/<name>.
const (
	instancePathLen   = 6
	instanceNameIndex = 5
)

// instanceName returns the name of the instance at path, a path in a
// cluster configuration, or "" when path is not an instance's.
func instanceName(path string) string {
	keys := goconfig.NewKeyPath(path)
	if len(keys) != instancePathLen ||
		keys[0] != "groups" || keys[2] != "replicasets" || keys[4] != "instances" {
		return ""
	}

	return keys[instanceNameIndex]
}

// Instances returns the names of the instances cfg declares, sorted. cfg is
// the Config of a [ClusterConfig]: one without the Tarantool inheritance
// hierarchy is an error.
func Instances(cfg goconfig.Config) ([]string, error) {
	all, err := cfg.EffectiveAll()
	if err != nil {
		return nil, fmt.Errorf("instances: %w", err)
	}

	names := make([]string, 0, len(all))

	for path := range all {
		name := instanceName(path)
		if name != "" {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names, nil
}

// InstanceConfig returns the configuration of the instance name in cfg, the
// Config of a [ClusterConfig], as Tarantool
// resolves it for that instance: what the instance sets, over what its
// replicaset sets, over its group's, over the global section. Credentials
// are merged across the levels rather than replaced.
//
// An instance cfg does not declare is an error wrapping [ErrNotFound]; a cfg
// without the Tarantool inheritance hierarchy is an error too.
func InstanceConfig(cfg goconfig.Config, name string) (goconfig.Config, error) {
	all, err := cfg.EffectiveAll()
	if err != nil {
		return goconfig.Config{}, fmt.Errorf("instance config: %w", err)
	}

	for path, instance := range all {
		if instanceName(path) == name {
			return instance, nil
		}
	}

	return goconfig.Config{}, fmt.Errorf("instance %q %w", name, ErrNotFound)
}
