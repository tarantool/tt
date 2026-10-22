package restore

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/backup/xlog"
)

// ConfigIdentity is what a cluster configuration says about who an instance
// is: the facts Tarantool's configuration holds the data directory to before
// it lets the instance start.
type ConfigIdentity struct {
	// ReplicasetName is the replicaset the configuration puts the instance in.
	ReplicasetName string
	// InstanceUUID and ReplicasetUUID are database.instance_uuid and
	// database.replicaset_uuid, empty when the configuration pins none.
	InstanceUUID   string
	ReplicasetUUID string
	// Anon is replication.anon. An anonymous replica cannot carry a name, so
	// a snapshot without one is no reason for it not to start.
	Anon bool
	// SkipNames is compat.skip_replication_names set to 'old', under which
	// Tarantool compares no names and requires none.
	SkipNames bool
	// EnvInstanceUUID and EnvReplicasetUUID are TT_DATABASE_INSTANCE_UUID and
	// TT_DATABASE_REPLICASET_UUID as tt's own environment has them. They say
	// nothing certain about the instance, whose environment is its own: a
	// matching one only turns a refusal into a warning.
	EnvInstanceUUID   string
	EnvReplicasetUUID string
}

// checkStartupNames holds the restored data to the check Tarantool's
// configuration makes before an instance starts on it, so that a restore that
// cannot come up is refused here rather than found later as a crash loop.
//
// That check reads the newest snapshot and nothing else: the instance UUID of
// its header, the name of the _cluster row carrying that UUID, and the
// replicaset UUID and name of its _schema. The journals after the snapshot do
// not count, because box.cfg replays them only once the check has passed. A
// snapshot taken before the members registered -- the bootstrap snapshot of a
// replicaset whose leader has taken no snapshot since -- holds the leader
// alone, and every other member restored from it has no name to start by,
// however surely the replay registers it.
//
// A mismatch between the snapshot and the configuration is refused. A name the
// snapshot lacks and the configuration does not stand in for with a UUID is
// refused too, unless tt's own environment carries the very UUID -- then it is
// warned about, since the instance starts only if its own environment carries
// it as well. Without a configuration the instance may never be started from
// one -- a box.cfg() of its own asks for no name -- so a missing name is
// warned about instead. Like the membership check, it runs on the rebuilt
// directories, and a refusal leaves no marker behind.
//
//nolint:err113 // Preserve established restore error messages.
func checkStartupNames(layout Layout, opts ApplyOpts) error {
	snapshot, ok, err := newestSnapshot(layout.Snapshot)
	if err != nil {
		return fmt.Errorf("failed to list the snapshots restored into %q: %w",
			layout.Snapshot, err)
	}

	if !ok {
		return nil
	}

	names, err := xlog.ReadSnapshotNames(snapshot)
	if err != nil {
		return err
	}

	if opts.Config != nil {
		problems := nameMismatches(names, *opts.Config, opts.InstanceName)

		if len(problems) > 0 {
			return fmt.Errorf("instance %q would not start on the restored data (%s): %s",
				opts.InstanceName, snapshot, strings.Join(problems, "; "))
		}
	}

	missing := missingNames(names, opts.Config)
	if len(missing) == 0 {
		return nil
	}

	finding := fmt.Sprintf("the snapshot %s records %s", snapshot, strings.Join(missing, ", "))
	if names.InstanceName == "" {
		finding += " -- " + replayNote(layout, snapshot, names.InstanceUUID)
	}

	if opts.Config == nil {
		log.Warnf("%s; an instance started from a cluster config refuses it: %s",
			finding, repairHints(names))

		return nil
	}

	if coveredByEnv(names, *opts.Config) {
		log.Warnf("%s; the uuid is taken from tt's environment: Tarantool does not "+
			"start the instance until its own environment carries it too (a unit "+
			"drop-in), and once it is up, run box.snapshot() on every node before the "+
			"variable is removed", finding)

		return nil
	}

	return fmt.Errorf("instance %q would not start on the restored data, which "+
		"Tarantool refuses at startup: %s%s; %s", opts.InstanceName, finding,
		envMismatch(names, *opts.Config), repairHints(names))
}

// nameMismatches lists what Tarantool's configuration would refuse the pair
// of a snapshot and a configuration over as contradicting one another, in the
// order it checks them.
func nameMismatches(names xlog.SnapshotNames, config ConfigIdentity, instanceName string) []string {
	var problems []string

	if config.ReplicasetUUID != "" && !sameUUID(config.ReplicasetUUID, names.ReplicasetUUID) {
		problems = append(problems, fmt.Sprintf(
			"the snapshot belongs to replicaset uuid %s, the config pins "+
				"database.replicaset_uuid %s", names.ReplicasetUUID, config.ReplicasetUUID))
	}

	if !config.SkipNames && names.ReplicasetName != "" &&
		names.ReplicasetName != config.ReplicasetName {
		problems = append(problems, fmt.Sprintf(
			"the snapshot names the replicaset %q, the config puts the instance in %q",
			names.ReplicasetName, config.ReplicasetName))
	}

	if config.InstanceUUID != "" && !sameUUID(config.InstanceUUID, names.InstanceUUID) {
		problems = append(problems, fmt.Sprintf(
			"the snapshot belongs to instance uuid %s, the config pins "+
				"database.instance_uuid %s", names.InstanceUUID, config.InstanceUUID))
	}

	if !config.SkipNames && names.InstanceName != "" && names.InstanceName != instanceName {
		problems = append(problems, fmt.Sprintf(
			"the snapshot binds instance uuid %s to the name %q",
			names.InstanceUUID, names.InstanceName))
	}

	return problems
}

// missingNames lists the names the snapshot lacks and the configuration does
// not stand in for with a UUID. Without a configuration every name counts:
// nothing says the instance is anonymous or skips names.
func missingNames(names xlog.SnapshotNames, config *ConfigIdentity) []string {
	if config != nil && (config.Anon || config.SkipNames) {
		return nil
	}

	var missing []string

	if names.InstanceName == "" && (config == nil || config.InstanceUUID == "") {
		missing = append(missing, fmt.Sprintf("no name for its instance uuid %s "+
			"(its _cluster holds %s)", names.InstanceUUID, describeCluster(names.Members)))
	}

	if names.ReplicasetName == "" && (config == nil || config.ReplicasetUUID == "") {
		missing = append(missing, "no name for its replicaset uuid "+names.ReplicasetUUID)
	}

	return missing
}

// coveredByEnv reports whether tt's environment carries every UUID the
// instance needs in place of the names the snapshot lacks.
func coveredByEnv(names xlog.SnapshotNames, config ConfigIdentity) bool {
	if names.InstanceName == "" && config.InstanceUUID == "" &&
		!sameUUID(config.EnvInstanceUUID, names.InstanceUUID) {
		return false
	}

	if names.ReplicasetName == "" && config.ReplicasetUUID == "" &&
		!sameUUID(config.EnvReplicasetUUID, names.ReplicasetUUID) {
		return false
	}

	return true
}

// envMismatch says which UUID of tt's environment names another instance or
// replicaset than the restored data does: the reason it does not count.
func envMismatch(names xlog.SnapshotNames, config ConfigIdentity) string {
	var notes []string

	if names.InstanceName == "" && config.EnvInstanceUUID != "" &&
		!sameUUID(config.EnvInstanceUUID, names.InstanceUUID) {
		notes = append(notes, fmt.Sprintf("TT_DATABASE_INSTANCE_UUID in tt's "+
			"environment is %s, not %s", config.EnvInstanceUUID, names.InstanceUUID))
	}

	if names.ReplicasetName == "" && config.EnvReplicasetUUID != "" &&
		!sameUUID(config.EnvReplicasetUUID, names.ReplicasetUUID) {
		notes = append(notes, fmt.Sprintf("TT_DATABASE_REPLICASET_UUID in tt's "+
			"environment is %s, not %s", config.EnvReplicasetUUID, names.ReplicasetUUID))
	}

	if len(notes) == 0 {
		return ""
	}

	return "; " + strings.Join(notes, "; ")
}

// repairHints names the ways out of a snapshot the instance cannot start by.
func repairHints(names xlog.SnapshotNames) string {
	variables := make([]string, 0, maxIdentityVars)

	if names.InstanceName == "" {
		variables = append(variables, "TT_DATABASE_INSTANCE_UUID="+names.InstanceUUID)
	}

	if names.ReplicasetName == "" {
		variables = append(variables, "TT_DATABASE_REPLICASET_UUID="+names.ReplicasetUUID)
	}

	return fmt.Sprintf("to recover, set %s in the instance's own environment (a "+
		"unit drop-in) and run box.snapshot() on every node once it is up, before "+
		"the variable is removed; or take the full backup with tt backup start "+
		"--snapshot, so that its snapshot names every member",
		strings.Join(variables, " and "))
}

// replayNote says what the replay makes of a UUID the snapshot has no name
// for. Tarantool does not look there at startup, but whether the journals
// register it -- and under which name -- tells a snapshot taken too early
// from a member the backup never had a name for.
func replayNote(layout Layout, snapshot, instanceUUID string) string {
	journals, err := sortedJournals(layout.WAL)
	if err != nil {
		return "the journals after it could not be listed"
	}

	members, err := xlog.MembershipAt(snapshot, journals)
	if err != nil {
		return "the journals after it could not be read"
	}

	for _, member := range members {
		if !sameUUID(member.UUID, instanceUUID) {
			continue
		}

		if member.Name == "" {
			return "the journals after it register the uuid without a name"
		}

		return fmt.Sprintf("the journals after it do register the uuid as %q, "+
			"but Tarantool reads the name from the snapshot alone", member.Name)
	}

	return "no journal after it registers the uuid either"
}

// sameUUID compares two UUIDs as UUIDs: the configuration and the files may
// spell one differently. Two strings that do not parse compare as strings.
func sameUUID(a, b string) bool {
	left, errLeft := uuid.Parse(a)
	right, errRight := uuid.Parse(b)

	if errLeft != nil || errRight != nil {
		return a == b
	}

	return left == right
}

const maxIdentityVars = 2
