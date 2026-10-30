package cmd

import (
	"errors"
	"fmt"
	"strings"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/restore"
)

const restoreWorkDirFlag = "--work-dir"
const restoreDirConfigKey = "dir"

// restoreLayoutFlags are the directory flags of `tt restore apply`, as typed.
type restoreLayoutFlags struct {
	// WorkDir is --work-dir: the directory to put everything in without
	// --config, and the directory the instance is launched from with it.
	WorkDir string
	// SnapshotDir, WALDir and VinylDir are the per-directory flags. Each
	// overrides its own directory and nothing else.
	SnapshotDir string
	WALDir      string
	VinylDir    string
	// Config is --config: the cluster configuration to read the directories
	// out of, a file or a URI.
	Config string
	// Instance is --instance: whose directories to read out of Config.
	Instance string
}

// overrides are the directories the per-directory flags name outright. An
// empty field is a directory nothing on the command line decided.
func (f restoreLayoutFlags) overrides() restore.Layout {
	return restore.Layout{
		Snapshot: f.SnapshotDir,
		WAL:      f.WALDir,
		Vinyl:    f.VinylDir,
	}
}

// restoreLayoutSources names where each resolved directory came from, so that
// the line apply prints before it touches the disk can be checked against what
// the operator meant.
type restoreLayoutSources struct {
	Snapshot string
	WAL      string
	Vinyl    string
}

// configDirsLoader reads the data directories of one instance out of a cluster
// configuration, leaving out the keys the override already answers. Apply
// passes clusterConfigDirs; it is a parameter so that the precedence between
// the flags can be exercised without a configuration.
type configDirsLoader func(
	configPath, instance string, override restore.Layout,
) (restore.ConfigDirs, error)

// resolveRestoreLayout works out which three directories a restore writes into.
//
// An explicit per-directory flag wins over what the cluster configuration
// says, which wins over --work-dir -- each step being more specific about this
// one restore than the one before it. Overrides are per directory, so
// --work-dir with a single --wal-dir puts the journal aside and leaves the
// rest where --work-dir says.
//
// A layout that cannot be completed is refused rather than half-applied: a
// restore that guessed at one of the three would write an instance's data
// somewhere nobody named, and the instance would start on whichever part of it
// the configured directories happen to hold.
func resolveRestoreLayout(flags restoreLayoutFlags, load configDirsLoader) (
	restore.Layout, restoreLayoutSources, error,
) {
	layout, sources, err := baseRestoreLayout(flags, load)
	if err != nil {
		return restore.Layout{}, restoreLayoutSources{}, err
	}

	layout, sources, err = overrideRestoreDirs(layout, sources, flags)
	if err != nil {
		return restore.Layout{}, restoreLayoutSources{}, err
	}

	// Resolved before the layout is reported or written into. A relative
	// directory names the same place only from the directory tt was started
	// in, and the report is what an operator checks the restore against.
	return layout.Resolved(), sources, nil
}

// baseRestoreLayout is what the layout is before the per-directory flags have
// their say: everything the cluster configuration names, or one directory for
// all three, or nothing at all when neither was given.
//
// The per-directory flags are handed to the configuration step all the same,
// because a key one of them replaces is never read and never has to resolve:
// refusing a call that named all three directories, over a configuration value
// none of them would have been read from, refuses a complete call.
func baseRestoreLayout(flags restoreLayoutFlags, load configDirsLoader) (
	restore.Layout, restoreLayoutSources, error,
) {
	var (
		layout  restore.Layout
		sources restoreLayoutSources
	)

	switch {
	case flags.Config != "" && flags.Instance == "":
		return layout, sources, fmt.Errorf("%w: --config needs --instance to say whose "+
			"directories to read out of it", restore.ErrValidation)
	case flags.Instance != "" && flags.Config == "":
		return layout, sources, fmt.Errorf("%w: --instance needs --config to say which "+
			"configuration to read it from", restore.ErrValidation)
	case flags.Config != "":
		dirs, err := load(flags.Config, flags.Instance, flags.overrides())
		if err != nil {
			return layout, sources, err
		}

		layout, err = restore.LayoutFromConfig(
			dirs, flags.Instance, flags.WorkDir, flags.overrides(),
		)
		if err != nil {
			return restore.Layout{}, sources, err
		}

		source := fmt.Sprintf("%s, instance %s", flags.Config, flags.Instance)

		sources = restoreLayoutSources{Snapshot: source, WAL: source, Vinyl: source}
	case flags.WorkDir != "":
		layout = restore.FlatLayout(flags.WorkDir)
		sources = restoreLayoutSources{
			Snapshot: restoreWorkDirFlag,
			WAL:      restoreWorkDirFlag,
			Vinyl:    restoreWorkDirFlag,
		}
	}

	return layout, sources, nil
}

// overrideRestoreDirs lets each per-directory flag replace its own directory,
// and refuses a layout that is still missing one.
func overrideRestoreDirs(
	layout restore.Layout,
	sources restoreLayoutSources,
	flags restoreLayoutFlags,
) (restore.Layout, restoreLayoutSources, error) {
	for _, dir := range []struct {
		kind     string
		flag     string
		override string
		value    *string
		source   *string
	}{
		{
			kind:     "snapshot",
			flag:     "--snapshot-dir",
			override: flags.SnapshotDir,
			value:    &layout.Snapshot,
			source:   &sources.Snapshot,
		},
		{
			kind:     "wal",
			flag:     "--wal-dir",
			override: flags.WALDir,
			value:    &layout.WAL,
			source:   &sources.WAL,
		},
		{
			kind:     "vinyl",
			flag:     "--vinyl-dir",
			override: flags.VinylDir,
			value:    &layout.Vinyl,
			source:   &sources.Vinyl,
		},
	} {
		if dir.override != "" {
			*dir.value, *dir.source = dir.override, dir.flag
		}

		if *dir.value == "" {
			return restore.Layout{}, restoreLayoutSources{},
				fmt.Errorf("%w: no %s directory given: pass %s, or --work-dir to put "+
					"every kind of file in one directory",
					restore.ErrValidation, dir.kind, dir.flag)
		}
	}

	return layout, sources, nil
}

// clusterConfigDirs reads the data directories of one instance out of a
// cluster configuration, which may be a file or a URI -- the same source
// `tt restore plan -c` takes.
//
// A file that cannot be read is a rejected input: a path that names nothing, a
// file that does not parse, a document that is not a cluster configuration are
// all things the caller can correct, and reporting them as such is what tells
// the caller to correct the call rather than to run it again. A configuration
// named by URI is the other case: a storage that does not answer is an
// operational failure of something else, and one that answers with something
// unreadable arrives here as the same failure, so neither is reported as a
// rejected input.
func clusterConfigDirs(
	configPath, instance string, override restore.Layout,
) (restore.ConfigDirs, error) {
	clusterConfig, err := loadRestoreClusterConfig(configPath)
	if err != nil {
		return restore.ConfigDirs{}, err
	}

	return instanceConfigDirs(clusterConfig, instance, override)
}

// loadRestoreClusterConfig reads the cluster configuration apply is given, a
// file or a URI, reporting a failure the way clusterConfigDirs explains.
func loadRestoreClusterConfig(configPath string) (goconfig.Config, error) {
	clusterConfig, _, err := loadTopologyConfig(&cmdCtx, configPath)
	if err != nil {
		_, uriErr := connect.CreateURIOpts(configPath)
		if uriErr != nil {
			return goconfig.Config{}, fmt.Errorf(
				"%w: failed to load the cluster config %q: %w",
				restore.ErrValidation, log.RedactTarget(configPath), err)
		}

		return goconfig.Config{}, fmt.Errorf(
			"failed to load the cluster config: %w", err)
	}

	return clusterConfig, nil
}

// identityEnv are the environment variables Tarantool's configuration takes
// an instance's UUIDs from ahead of the cluster configuration. Apply can read
// only its own environment, not the instance's, so what it finds there is no
// proof: a matching UUID only turns a refusal into a warning.
var identityEnv = struct {
	instanceUUID   string
	replicasetUUID string
}{
	instanceUUID:   "TT_DATABASE_INSTANCE_UUID",
	replicasetUUID: "TT_DATABASE_REPLICASET_UUID",
}

// instanceConfigIdentity picks out of an instance's configuration what
// Tarantool holds a data directory to before the instance starts on it: the
// replicaset the instance is in, the UUIDs the configuration pins, and the two
// settings under which names are not required. A key the configuration does
// not set is left empty; one holding a value of the wrong type is refused.
func instanceConfigIdentity(
	clusterConfig goconfig.Config,
	instance string,
	getenv func(string) string,
) (*restore.ConfigIdentity, error) {
	_, replicaset, found := cluster.FindInstance(clusterConfig, instance)
	if !found {
		return nil, fmt.Errorf(
			"%w: the cluster config declares no instance %q", restore.ErrValidation, instance)
	}

	instanceConfig, err := cluster.InstanceConfig(clusterConfig, instance)
	if err != nil {
		return nil, fmt.Errorf("%w: instance %q: %w", restore.ErrValidation, instance, err)
	}

	identity := new(restore.ConfigIdentity)

	identity.ReplicasetName = replicaset

	var skipNames string

	for _, key := range []struct {
		path []string
		dst  any
	}{
		{path: []string{"database", "instance_uuid"}, dst: &identity.InstanceUUID},
		{path: []string{"database", "replicaset_uuid"}, dst: &identity.ReplicasetUUID},
		{path: []string{"replication", "anon"}, dst: &identity.Anon},
		{path: []string{"compat", "skip_replication_names"}, dst: &skipNames},
	} {
		err := configValue(instanceConfig, instance, key.path, key.dst)
		if err != nil {
			return nil, err
		}
	}

	identity.SkipNames = skipNames == "old"

	identity.EnvInstanceUUID = getenv(identityEnv.instanceUUID)
	identity.EnvReplicasetUUID = getenv(identityEnv.replicasetUUID)

	// tt merges the TT_ variables of its own environment into a configuration
	// it reads from a file, so a UUID equal to the variable may be tt's
	// environment rather than the configuration speaking. It is counted as the
	// environment's: at worst a configuration that pins the same UUID costs a
	// warning, where the other way round a crash loop would pass unnoticed.
	if identity.EnvInstanceUUID != "" && identity.InstanceUUID == identity.EnvInstanceUUID {
		identity.InstanceUUID = ""
	}

	if identity.EnvReplicasetUUID != "" &&
		identity.ReplicasetUUID == identity.EnvReplicasetUUID {
		identity.ReplicasetUUID = ""
	}

	return identity, nil
}

// configValue reads one key of an instance's configuration into dst, a
// *string or a *bool, leaving dst alone when the key is not set.
func configValue(config goconfig.Config, instance string, path []string, dst any) error {
	value, err := restoreConfigValue(config, path)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return nil
		}

		return fmt.Errorf("%w: %s of instance %q: %w",
			restore.ErrValidation, strings.Join(path, "."), instance, err)
	}

	ok := false

	switch dst := dst.(type) {
	case *string:
		*dst, ok = value.(string)
	case *bool:
		*dst, ok = value.(bool)
	}

	if !ok {
		return fmt.Errorf("%w: %s of instance %q is %v, of the wrong type",
			restore.ErrValidation, strings.Join(path, "."), instance, value)
	}

	return nil
}

// instanceConfigDirs picks the three directory keys out of an instance's
// configuration.
//
// The configuration is instantiated first, so a key set for the whole cluster,
// the group or the replicaset reaches the instance that inherits it -- which
// is how a deployment states one layout for every one of its instances, with
// {{ instance_name }} doing the telling apart.
//
// A key the override names is not read at all, and what the configuration puts
// there is neither resolved nor checked: the caller has answered that question
// on the command line, and a call that names every directory it needs is
// complete whatever the configuration says about the same keys.
// process.work_dir goes with the three, because it is only what a relative one
// of them is taken against: with all three named outright, nothing is left for
// it to resolve.
func instanceConfigDirs(
	clusterConfig goconfig.Config,
	instance string,
	override restore.Layout,
) (restore.ConfigDirs, error) {
	if !cluster.HasInstance(clusterConfig, instance) {
		return restore.ConfigDirs{}, fmt.Errorf(
			"%w: the cluster config declares no instance %q", restore.ErrValidation, instance)
	}

	instanceConfig, err := cluster.InstanceConfig(clusterConfig, instance)
	if err != nil {
		return restore.ConfigDirs{}, fmt.Errorf("%w: instance %q: %w",
			restore.ErrValidation, instance, err)
	}

	var dirs restore.ConfigDirs

	everyDirNamed := override.Snapshot != "" && override.WAL != "" && override.Vinyl != ""

	for _, key := range []struct {
		path []string
		skip bool
		dst  *string
	}{
		{
			path: []string{"snapshot", restoreDirConfigKey},
			skip: override.Snapshot != "", dst: &dirs.Snapshot,
		},
		{path: []string{"wal", restoreDirConfigKey}, skip: override.WAL != "", dst: &dirs.WAL},
		{
			path: []string{"vinyl", restoreDirConfigKey},
			skip: override.Vinyl != "", dst: &dirs.Vinyl,
		},
		{path: []string{"process", "work_dir"}, skip: everyDirNamed, dst: &dirs.ProcessWorkDir},
	} {
		if key.skip {
			continue
		}

		value, err := restoreConfigValue(instanceConfig, key.path)
		if err != nil {
			// A key the configuration does not set is not an error: Tarantool
			// has a default for each of them, and LayoutFromConfig fills it
			// in. Anything else is the configuration holding something else
			// where that key belongs -- a scalar in place of the map, say --
			// and filling in the default over it would put an instance's data
			// somewhere the instance does not read it from.
			if errors.Is(err, goconfig.ErrKeyNotFound) {
				continue
			}

			return restore.ConfigDirs{}, fmt.Errorf("%w: %s of instance %q: %w",
				restore.ErrValidation, strings.Join(key.path, "."), instance, err)
		}

		text, ok := value.(string)
		if !ok {
			return restore.ConfigDirs{}, fmt.Errorf(
				"%w: %s of instance %q is %v, not a path",
				restore.ErrValidation, strings.Join(key.path, "."), instance, value)
		}

		*key.dst = text
	}

	return dirs, nil
}

// restoreConfigValue distinguishes an absent key from a scalar in place of one
// of its containing sections. Both appear as ErrKeyNotFound in go-config, but
// only an absent key may fall back to Tarantool's default directory.
func restoreConfigValue(config goconfig.Config, path []string) (any, error) {
	var value any

	_, err := config.Get(goconfig.KeyPath(path), &value)
	if err == nil {
		return value, nil
	}

	if !errors.Is(err, goconfig.ErrKeyNotFound) {
		return value, fmt.Errorf("read configuration key %s: %w", strings.Join(path, "."), err)
	}

	for length := 1; length < len(path); length++ {
		var parent any

		_, parentErr := config.Get(goconfig.KeyPath(path[:length]), &parent)

		if errors.Is(parentErr, goconfig.ErrKeyNotFound) {
			break
		}

		if parentErr != nil {
			return nil, fmt.Errorf("read configuration section: %w", parentErr)
		}

		switch parent.(type) {
		case map[string]any, map[any]any:
		default:
			return nil, fmt.Errorf("%w: %s is %v, of the wrong type",
				restore.ErrValidation, strings.Join(path[:length], "."), parent)
		}
	}

	return nil, fmt.Errorf("read configuration key %s: %w", strings.Join(path, "."), err)
}
