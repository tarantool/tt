package replicaset

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

// RebootstrapCtx contains rebootstrap operations arguments.
type RebootstrapCtx struct {
	// Instance is the instance to re-bootstrap, as the instance commands
	// resolve it.
	Instance running.InstanceCtx
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
		foundFiles, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("cannot search for %q: %w", pattern, err)
		}

		filesToRemove = append(filesToRemove, foundFiles...)
	}

	for _, fileToRemove := range filesToRemove {
		stat, err := os.Stat(fileToRemove)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return fmt.Errorf("cannot get info of %q: %w", fileToRemove, err)
		}

		if stat.Mode().IsRegular() {
			err = os.Remove(fileToRemove)
			if err != nil {
				return fmt.Errorf("cannot remove %q: %w", fileToRemove, err)
			}

			log.Debugf("Removed %q", fileToRemove)
		}
	}

	return nil
}

// Rebootstrap re-bootstraps the instance by stopping it, removing its artifacts,
// and starting it again.
func Rebootstrap(cmdCtx cmdcontext.CmdCtx, rbCtx RebootstrapCtx) error {
	instCtx := rbCtx.Instance

	if !rbCtx.Confirmed {
		yes, err := util.AskConfirm(os.Stdin, fmt.Sprintf(
			"Rebootstrap will stop the instance %s and remove all its data files. "+
				"Do you want to continue?",
			instCtx.InstName))
		if err != nil || !yes {
			return err
		}
	}

	log.Debugf("Stopping the instance")

	err := running.Stop(&instCtx)
	if err != nil {
		return fmt.Errorf("failed to stop the instance %s: %w", instCtx.InstName, err)
	}

	err = cleanDataFiles(instCtx)
	if err != nil {
		return fmt.Errorf("failed to remove instance's artifacts: %w", err)
	}

	// TODO: need to support integrity check continuation on this start.
	// tarantool/tt-ee#203.
	log.Debugf("Starting the instance")

	ttBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find the tt executable: %w", err)
	}

	err = running.StartWatchdog(&cmdCtx, ttBin, instCtx, []string{})
	if err != nil {
		return fmt.Errorf("failed to start the instance: %w", err)
	}

	return nil
}
