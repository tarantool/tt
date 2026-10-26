// Package systemd writes the systemd units that run the instances of an
// application installed in the system scope, and the mark that tells the
// instance commands which instances the units manage (decisions 20 and 39).
// It takes what the units are made of and knows nothing of how tt finds an
// application or runs an instance.
//
// # Units
//
// An application gets one template unit, <app>@.service. Its ExecStart runs
// tt instance start --foreground <app>:%i: tt stays in the foreground while
// the instance runs under a watchdog that never restarts it, and exits when
// the instance does (decision 37), so the restart is up to systemd. The unit
// gives tt the root of the system scope in TT_SYSTEM_ROOT, since tt run by
// systemd would look for the application in the default root otherwise, and
// its own name in TT_SYSTEMD_UNIT (UnitEnv), by which tt tells the start the
// unit asks for from a start next to the unit.
//
// A template is one for all its instances, so what differs between them goes
// into a drop-in of every instance, <app>@<inst>.service.d/tt.conf: Restart=
// from the restart field of its record, whose values are those of systemd,
// and TimeoutStopSec= from its stop_timeout. The template carries the
// defaults of both for an instance without a drop-in.
//
// tt stops the instance itself: it sends the instance its stop signal and
// kills it once its stop_timeout is up (decision 31). So the stop signal of
// systemd, SIGTERM, goes to tt alone (KillMode=mixed), and TimeoutStopSec=
// leaves tt its stop_timeout and a margin to kill the instance and exit;
// SIGKILL to the whole unit comes after that, or once tt has exited, for what
// is left.
//
// # Mark
//
// tt service systemd install writes the units and the mark, <root>/etc/<app>/
// systemd.yaml (MarkFileName): the unit directory, the template, the user and
// the group the units run the instances as, which an install that names none
// keeps, and the unit of every instance the units manage. tt instance start,
// stop, restart and kill refuse those instances, pointing at systemctl: a
// watchdog of tt next to the unit would supervise the instance twice, and a
// stop behind the back of the unit would leave it out of step.
// tt service systemd uninstall removes the units and the mark.
//
// The files tt writes start with GeneratedHeader. install overwrites only
// such files, and uninstall removes only such files: a unit of the same name
// written by someone else is not tt's to replace or remove.
package systemd
