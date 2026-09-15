package restore

import (
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/util/regexputil"
)

// defaultDataDir is where Tarantool keeps every kind of data when the
// configuration names no directory of its own. It is the default of
// snapshot.dir, wal.dir and vinyl.dir alike, which is why an instance that
// configures none of them has all three in one place.
const defaultDataDir = "var/lib/{{ instance_name }}"

// instanceNameVar is the template variable a data directory may carry. It is
// the only one Tarantool substitutes into a path that differs per instance,
// and it is what makes one configured directory serve a whole replicaset.
const instanceNameVar = "instance_name"

// ConfigDirs are the data directories as a cluster configuration spells them,
// unresolved: the values of snapshot.dir, wal.dir and vinyl.dir, and the
// process.work_dir a relative one of them is taken against. An empty field is
// a key the configuration does not set.
type ConfigDirs struct {
	Snapshot       string
	WAL            string
	Vinyl          string
	ProcessWorkDir string
}

// LayoutFromConfig turns what a cluster configuration says about an instance
// into the directories a restore writes into.
//
// It follows Tarantool's own resolution, so that the restored files land where
// the instance will look for them: a key the configuration omits defaults to
// var/lib/{{ instance_name }}, {{ instance_name }} is substituted, a relative
// directory is taken against process.work_dir, and a relative (or absent)
// process.work_dir against workDir -- the directory Tarantool is launched
// from, which no configuration can state and the caller has to supply.
// Anything left relative with no workDir to resolve it against is refused
// rather than guessed at, naming the key that could not be resolved.
//
// A non-empty field of override is a directory the caller has already decided,
// and is taken as it stands: the configuration's value for that key is neither
// read nor required to resolve. A caller who named a directory outright has
// answered the question the configuration would have answered, and refusing
// the run because another spelling of that same answer cannot be resolved
// would refuse a call that is complete. process.work_dir follows from that: it
// is only ever what a relative directory is taken against, so a call whose
// surviving directories are all absolute never reads it.
func LayoutFromConfig(
	dirs ConfigDirs, instanceName, workDir string, override Layout,
) (Layout, error) {
	if instanceName == "" {
		return Layout{}, fmt.Errorf("%w: no instance name given to resolve the "+
			"directories of", ErrValidation)
	}

	resolver := configResolver{
		instanceName:   instanceName,
		processWorkDir: dirs.ProcessWorkDir,
		workDir:        workDir,
	}

	layout := Layout{}

	for _, dir := range []struct {
		key      string
		value    string
		override string
		dst      *string
	}{
		{
			key:      "snapshot.dir",
			value:    dirs.Snapshot,
			override: override.Snapshot,
			dst:      &layout.Snapshot,
		},
		{key: "wal.dir", value: dirs.WAL, override: override.WAL, dst: &layout.WAL},
		{
			key:      "vinyl.dir",
			value:    dirs.Vinyl,
			override: override.Vinyl,
			dst:      &layout.Vinyl,
		},
	} {
		if dir.override != "" {
			*dir.dst = dir.override

			continue
		}

		resolved, err := resolver.resolve(dir.key, dir.value)
		if err != nil {
			return Layout{}, err //nolint:wrapcheck
		}

		*dir.dst = resolved
	}

	return layout, nil
}

// configResolver turns one configured directory into an absolute one. It holds
// what the base of a relative directory is worked out from: process.work_dir
// as the configuration spells it, and the launch directory the caller supplied.
type configResolver struct {
	instanceName   string
	processWorkDir string
	workDir        string
}

// base returns the directory a relative configured path is taken against,
// together with the name of what it came from so that a path which cannot be
// resolved says why. An empty base is nothing to resolve against.
//
// It is worked out here, per directory that needs it, rather than once up
// front: a configuration whose directories are absolute -- or whose keys the
// caller named outright -- never asks what the launch directory is, and
// process.work_dir is then a value nothing reads. Refusing such a call over
// what that value says would refuse a call that is complete.
func (r configResolver) base() (string, string, error) {
	processWorkDir, err := r.render("process.work_dir", r.processWorkDir)
	if err != nil {
		return "", "", err //nolint:wrapcheck
	}

	switch {
	case processWorkDir == "":
		// Tarantool launched without process.work_dir runs in the directory it
		// was started from, which is what --work-dir stands in for here.
		return r.workDir, "--work-dir", nil
	case filepath.IsAbs(processWorkDir):
		return processWorkDir, "process.work_dir", nil
	case r.workDir != "":
		return filepath.Join(r.workDir, processWorkDir),
			"process.work_dir under --work-dir", nil
	default:
		// A relative process.work_dir with nothing to take it against.
		return "", fmt.Sprintf("process.work_dir %q", processWorkDir), nil
	}
}

// resolve turns one configured value into an absolute directory, filling in
// the default for a key the configuration does not set.
func (r configResolver) resolve(key, value string) (string, error) {
	if value == "" {
		value = defaultDataDir
	}

	rendered, err := r.render(key, value)
	if err != nil {
		return "", err //nolint:wrapcheck
	}

	if filepath.IsAbs(rendered) {
		return rendered, nil
	}

	base, baseKey, err := r.base()
	if err != nil {
		return "", err //nolint:wrapcheck
	}

	if base == "" {
		return "", fmt.Errorf("%w: %s is %q, which is relative to %s: pass --work-dir "+
			"to say which directory the instance is launched from",
			ErrValidation, key, rendered, baseKey)
	}

	return filepath.Join(base, rendered), nil
}

// render substitutes {{ instance_name }}, and refuses a value carrying any
// other variable rather than passing the braces through as part of a path.
func (r configResolver) render(key, value string) (string, error) {
	if value == "" {
		return "", nil
	}

	rendered, err := regexputil.ApplyVars(value, map[string]string{
		instanceNameVar: r.instanceName,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %s %q: %w", ErrValidation, key, value, err)
	}

	return rendered, nil
}
