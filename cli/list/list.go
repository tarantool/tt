package list

import (
	"fmt"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

// ListInstances shows enabled applications.
func ListInstances(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) error {
	if _, err := os.Stat(cliOpts.App.InstancesEnabled); os.IsNotExist(err) {
		return fmt.Errorf("instances enabled directory doesn't exist: %s",
			cliOpts.App.InstancesEnabled)
	}

	appList, err := util.CollectAppList(cmdCtx.Cli.ConfigDir,
		cliOpts.App.InstancesEnabled, false)
	if err != nil {
		return fmt.Errorf("can't collect an application list: %s", err)
	}

	if len(appList) == 0 {
		log.Info("there are no enabled applications")
	}

	fmt.Println("List of enabled applications:")

	for _, app := range appList {
		log.Infof("%s", color.GreenString(strings.TrimSuffix(app.Name, ".lua")))
		instances, _ := running.CollectInstances(app.Name, cliOpts.App.InstancesEnabled)
		for _, inst := range instances {
			fullInstanceName := running.GetAppInstanceName(inst)
			if fullInstanceName != app.Name {
				fmt.Printf("	%s\n",
					color.YellowString(strings.TrimPrefix(fullInstanceName, app.Name+":")))
			}
		}
	}

	return nil
}
