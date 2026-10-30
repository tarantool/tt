package running

import (
	"errors"
	"fmt"
	"os"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
)

// ErrNotClusterInstance is wrapped by the error for an instance given to a
// command that acts on the instances of the cluster config, such as
// tt cluster show, while no cluster config configures it: an instance that
// runs its script, or an external process.
var ErrNotClusterInstance = errors.New("not an instance of the cluster config")

var (
	// errNoScript is wrapped by the error for an instance whose script is
	// not a file tarantool can run.
	errNoScript = errors.New("cannot run the script")
	// errIsDirectory is wrapped by the error for a script that is a
	// directory.
	errIsDirectory = errors.New("is a directory")
)

// IsScript reports whether inst is a Tarantool instance that runs its script,
// a record with script, which configures it with box.cfg rather than the
// cluster config.
func IsScript(inst *InstanceCtx) bool {
	return inst.Kind == instances.KindScript
}

// RequireClusterRecord returns an error when the record of application named
// name is not configured by the cluster config: one wrapping ErrNotTarantool
// for an external process, as RequireTarantoolRecord does, and one wrapping
// ErrNotClusterInstance for an instance that runs its script. Like
// RequireTarantoolRecord, it passes a name the description does not declare.
func RequireClusterRecord(application *app.App, name string) error {
	err := RequireTarantoolRecord(application, name)
	if err != nil {
		return err
	}

	for _, record := range application.Instances {
		if record.Name == name && record.Kind() == instances.KindScript {
			return fmt.Errorf("%s%c%s runs its script (script: %s), and no cluster config "+
				"configures it: %w", application.Name, InstanceDelimiter, name, record.Script,
				ErrNotClusterInstance)
		}
	}

	return nil
}

// setScriptCtx makes instCtx, built for the record inst with the paths of a
// Tarantool instance, the context of the Tarantool instance that runs the
// script of inst. The instance has no cluster config: its script configures
// it with box.cfg, which takes the paths of the layout from tt for the
// options it leaves out (scriptSpec). The paths the TT_ variables of those
// options in the env of the record name win over the layout, as they win
// in box.cfg, and setInstCtxFromEffectiveConfig takes them in.
func setScriptCtx(instCtx *InstanceCtx, inst instances.Instance) {
	instCtx.InstanceScript = inst.Script
	instCtx.ClusterConfigPath = ""
	instCtx.Configuration = nil
}

// checkScript checks that the script of an instance that runs one is a file:
// a script that is missing, as after a typo in the description, is reported
// by the command that starts the instance rather than by tarantool in the
// log of the watchdog. Whether the script runs is up to tarantool.
func checkScript(inst *InstanceCtx) error {
	info, err := os.Stat(inst.InstanceScript)
	if err == nil && info.IsDir() {
		err = fmt.Errorf("%s %w", inst.InstanceScript, errIsDirectory)
	}

	if err == nil {
		// Tarantool runs it, and checks it with the hashes file of the
		// application; tt refuses one the signature does not list at all,
		// such as a script outside the package.
		return checkSigned(inst, "script", inst.InstanceScript)
	}

	if err != nil {
		return fmt.Errorf("instance %q of %q: %w: %w", inst.InstName, inst.AppName,
			errNoScript, err)
	}

	return nil
}
