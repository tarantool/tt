package running

import (
	"time"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/internal/systemd"
)

const (
	// DefaultStopTimeout is how long the watchdog lets an instance whose
	// record sets no stop_timeout take to stop before it kills it.
	DefaultStopTimeout = instanceStopTimeout
	// StopMargin is what tt instance stop waits for the watchdog of an
	// instance over its stop timeout: the time the watchdog takes to kill the
	// instance that outlived it, and to exit.
	StopMargin = process_utils.TerminationTimeout - instanceStopTimeout
)

// AttachUnits sets Unit of every instance of insts that a systemd unit
// manages: one of an application of the system scope whose mark of the units,
// <root>/etc/<app>/systemd.yaml, names it (decision 20). Other scopes have no
// units (decision 39). The mark of every application is read once; a mark
// that cannot be read is an error, since the instances it may name cannot be
// told apart then.
//
// It is the commands that act on instances that ask, when they resolve their
// targets: the watchdog does not ask again before it restarts its instance
// (reload), and a mark broken meanwhile does not keep it from doing so.
func AttachUnits(insts []InstanceCtx) error {
	marks := map[string]systemd.Mark{}

	for i := range insts {
		inst := &insts[i]
		if inst.Scope != app.ScopeSystem {
			continue
		}

		hostDir := app.HostDir(inst.Root, inst.AppName)

		mark, read := marks[hostDir]
		if !read {
			var err error

			mark, _, err = systemd.ReadMark(hostDir)
			if err != nil {
				return err
			}

			marks[hostDir] = mark
		}

		inst.Unit = mark.Instances[inst.InstName]
	}

	return nil
}

// StopTimeoutOf returns how long the watchdog of inst lets it take to stop
// before it kills it: stop_timeout of its record, or the default.
func StopTimeoutOf(inst *InstanceCtx) time.Duration {
	if inst.StopTimeout > 0 {
		return inst.StopTimeout
	}

	return DefaultStopTimeout
}

// PIDFile returns the pid file of the watchdog of the instance inst of the
// application appName under root, the root of its scope. It is for what
// knows an instance by its name alone, such as an instance that the
// description of its application no longer has.
func PIDFile(root, appName, inst string) (string, error) {
	scopeLayout, err := layout.NewScopeLayout(root, appName, inst)
	if err != nil {
		return "", err
	}

	return scopeLayout.PidFile(), nil
}
