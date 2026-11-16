package running

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	"github.com/tarantool/tt/v3/internal/pidfile"
)

// appDirPerm is the mode of the record of the application directory: where
// an instance runs from is no secret, and the run directory guards it.
const appDirPerm = 0o644

// appDirFile returns the record of the application directory of inst in its
// run directory (scope.AppDirFileName).
func appDirFile(inst *InstanceCtx) string {
	return filepath.Join(inst.RunDir, scope.AppDirFileName)
}

// recordAppDir writes the application directory inst runs from into its run
// directory, for whoever asks which version a running instance runs
// (RunningAppDir). The watchdog writes it every time it starts the instance,
// as it owns the pid file then; the directory of an installed application is
// resolved already, the version <root>/apps/<app> selected when the
// application was loaded, and stays the same over the restarts of one
// watchdog.
//
// The record replaces the previous one in one rename, so a reader gets one
// whole directory or the other.
func recordAppDir(inst *InstanceCtx) error {
	if inst.RunDir == "" || inst.AppDir == "" {
		return nil
	}

	path := appDirFile(inst)

	file, err := os.CreateTemp(inst.RunDir, "."+scope.AppDirFileName+"-*")
	if err != nil {
		return fmt.Errorf("cannot record the application directory: %w", err)
	}

	name := file.Name()

	_, err = file.WriteString(inst.AppDir + "\n")

	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(name, appDirPerm)
	}

	if err == nil {
		err = os.Rename(name, path)
	}

	if err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("cannot record the application directory in %s: %w", path, err)
	}

	return nil
}

// removeAppDir removes the record of the application directory of inst, as
// its watchdog ends.
func removeAppDir(inst *InstanceCtx) error {
	if inst.RunDir == "" {
		return nil
	}

	err := os.Remove(appDirFile(inst))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove the record of the application directory: %w", err)
	}

	return nil
}

// RunningAppDir returns the application directory the running instance inst
// runs from, as its watchdog recorded it when it started the instance
// (recordAppDir), and whether it is known. It is not while no watchdog holds
// the pid file of the instance, since a record left by a watchdog that died
// tells nothing, nor for an instance a tt that kept no record started.
//
// For an application of the user or the system scope it is the version the
// instance runs, which differs from inst.AppDir, the version active now,
// after a newer one has been installed or another one switched to and until
// the instance is restarted.
func RunningAppDir(inst *InstanceCtx) (string, bool) {
	_, owned, err := pidfile.Owner(inst.PIDFile)
	if err != nil || !owned {
		return "", false
	}

	data, err := os.ReadFile(appDirFile(inst))
	if err != nil {
		return "", false
	}

	dir := strings.TrimSpace(string(data))

	return dir, dir != ""
}

// RunningVersion returns the version of the application of the user or the
// system scope the running instance inst runs, as the VERSION of the
// directory its watchdog recorded (RunningAppDir), and the version active
// now, that of inst.AppDir; ok is false when the version it runs is not
// known. A project has one version, which its developer edits in place, so
// its instances have none.
func RunningVersion(inst *InstanceCtx) (string, string, bool) {
	if inst.Scope != app.ScopeUser && inst.Scope != app.ScopeSystem {
		return "", "", false
	}

	dir, ok := RunningAppDir(inst)
	if !ok {
		return "", "", false
	}

	runs := state.DirVersion(dir)

	return runs, state.DirVersion(inst.AppDir), runs != ""
}
