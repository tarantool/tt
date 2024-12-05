package instances

import (
	"fmt"
	"path/filepath"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

// ListInstances shows enabled applications.
func ListInstances(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) error {
	if !util.IsDir(cliOpts.Env.InstancesEnabled) {
		return fmt.Errorf("instances enabled directory doesn't exist")
	}

	appList, err := util.CollectAppList(cmdCtx.Cli.ConfigDir, cliOpts.Env.InstancesEnabled, false)
	if err != nil {
		return fmt.Errorf("can't collect applications list: %s", err)
	}

	if len(appList) == 0 {
		log.Info("there are no enabled applications")
	}

	fmt.Println("List of enabled applications:")
	fmt.Printf("instances enabled directory: %s\n", cliOpts.Env.InstancesEnabled)

	applications, err := running.CollectInstancesForApps(appList, cliOpts, cmdCtx.Cli.ConfigDir,
		cmdCtx.Integrity, false)
	if err != nil {
		return err
	}
	for appName, instances := range applications {
		if len(instances) == 0 {
			log.Warnf("no instances for %q application", appName)
			continue
		}
		inst := instances[0]
		appLocation := filepath.Base(running.GetAppPath(inst))
		if inst.IsFileApp {
			appLocation += string(filepath.Separator)
		}
		log.Infof("%s (%s)", color.GreenString(appName), appLocation)
		for _, inst := range instances {
			fullInstanceName := running.GetAppInstanceName(inst)
			if fullInstanceName != appName {
				script := ""
				if inst.InstanceScript != "" {
					script = filepath.Base(inst.InstanceScript)
				}
				fmt.Printf("	%s (%s)\n", color.YellowString(inst.InstName), script)
			}
		}
	}

	return nil
}
