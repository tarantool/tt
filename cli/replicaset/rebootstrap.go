package replicaset

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

// RebootstrapCtx contains rebootstrap operations arguments.
type RebootstrapCtx struct {
	// AppName is an application name the instance belongs to.
	AppName string
	// InstanceName is an instance name to re-bootstrap.
	InstanceName string
	// Confirmed is true if re-bootstrap confirmation is not required.
	Confirmed bool
}

// cleanDataFiles removes snap, xlog and vinyl artifacts.
func cleanDataFiles(instCtx running.InstanceCtx) error {
	filesToRemove := []string{}
	for _, pattern := range [...]string{
		filepath.Join(instCtx.MemtxDir, "*.snap"),
		filepath.Join(instCtx.WalDir, "*.xlog"),
		filepath.Join(instCtx.VinylDir, "*.vylog"),
	} {
		if foundFiles, err := filepath.Glob(pattern); err != nil {
			return err
		} else {
			filesToRemove = append(filesToRemove, foundFiles...)
		}
	}

	for _, fileToRemove := range filesToRemove {
		stat, err := os.Stat(fileToRemove)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("cannot get info of %q: %s", fileToRemove, err)
		}
		if stat.Mode().IsRegular() {
			if err = os.Remove(fileToRemove); err != nil {
				return fmt.Errorf("cannot remove %q: %s", fileToRemove, err)
			}
			log.Debugf("Removed %q", fileToRemove)
		}
	}

	return nil
}

// Rebootstrap re-bootstraps the instance by stopping it, removing its artifacts,
// and starting it again.
func Rebootstrap(cmdCtx cmdcontext.CmdCtx, cliOpts config.CliOpts, rbCtx RebootstrapCtx) error {
	apps, err := running.CollectInstancesForApps([]string{rbCtx.AppName}, &cliOpts,
		cmdCtx.Cli.ConfigDir, cmdCtx.Integrity, true)
	if err != nil {
		return fmt.Errorf("cannot collect application instances info: %s", err)
	}

	found := false
	var instCtx running.InstanceCtx
	for _, instCtx = range apps[rbCtx.AppName] {
		if instCtx.InstName == rbCtx.InstanceName {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("instance %q is not found", rbCtx.InstanceName)
	}

	if !rbCtx.Confirmed {
		if yes, err := util.AskConfirm(os.Stdin, fmt.Sprintf(
			"Rebootstrap will stop the instance %s and remove all its data files. "+
				"Do you want to continue?",
			rbCtx.InstanceName)); err != nil || !yes {
			return err
		}
	}

	log.Debugf("Stopping the instance")
	if err = running.Stop(&instCtx); err != nil {
		return fmt.Errorf("failed to stop the instance %s: %s", rbCtx.InstanceName, err)
	}

	if err = cleanDataFiles(instCtx); err != nil {
		return fmt.Errorf("failed to remove instance's artifacts: %s", err)
	}

	// TODO: need to support integrity check continuation on this start.
	// tarantool/tt-ee#203
	log.Debugf("Starting the instance")
	ttBin, err := os.Executable()
	if err != nil {
		return err
	}
	if err = running.StartWatchdog(&cmdCtx, ttBin, instCtx, []string{}); err != nil {
		return fmt.Errorf("failed to start the instance: %s", err)
	}

	return nil
}
