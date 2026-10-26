package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/systemd"
)

// errManagedBySystemd is wrapped by the error for instances that systemd
// units manage, which a command refuses (refuseManaged).
var errManagedBySystemd = errors.New("managed by systemd")

// managed returns the instances of insts that systemd units manage, but for
// those exempt keeps, and their units.
func managed(insts []running.InstanceCtx, exempt func(inst *running.InstanceCtx) bool) (
	[]string, []string,
) {
	var names, units []string

	for idx := range insts {
		inst := &insts[idx]
		if inst.Unit == "" || exempt != nil && exempt(inst) {
			continue
		}

		names = append(names, running.GetAppInstanceName(*inst))
		units = append(units, inst.Unit)
	}

	return names, units
}

// refuseManaged fails for the instances of insts that systemd units manage,
// as tt service systemd install recorded them, before the command acts on
// any instance: tt instance start, restart and stop would supervise an
// instance next to its unit, or stop it behind the back of the unit, which
// then is out of step with it (decision 20); tt instance kill too. The error
// names the units and the systemctl command, systemctl, that does it: such
// as "stop" for systemctl stop <unit>. exempt keeps an instance the command
// may act on all the same; nil keeps none.
//
// tt instance status, logs, logrotate, check, clean and connect act on such
// instances as on any: they neither start nor stop them.
func refuseManaged(insts []running.InstanceCtx, systemctl string,
	exempt func(inst *running.InstanceCtx) bool,
) error {
	names, units := managed(insts, exempt)

	switch len(names) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("%s is %w through the unit %s: use systemctl %s %s", names[0],
			errManagedBySystemd, units[0], systemctl, units[0])
	default:
		return fmt.Errorf("%s are %w through their units: use systemctl %s %s",
			strings.Join(names, ", "), errManagedBySystemd, systemctl, strings.Join(units, " "))
	}
}

// startedByItsUnit reports whether tt instance start runs inst as its unit
// asks: with --foreground, from the unit of the instance, which sets
// systemd.UnitEnv to its own name. The unit runs tt instance start
// --foreground, and the start must not refuse the very unit it is for.
//
// The variable comes from the unit rather than from systemd itself:
// INVOCATION_ID and JOURNAL_STREAM tell that some unit runs tt, not which.
func startedByItsUnit(inst *running.InstanceCtx) bool {
	return startForeground && inst.Unit != "" && os.Getenv(systemd.UnitEnv) == inst.Unit
}
