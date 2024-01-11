package instances

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
	instanceDir := cliOpts.Env.InstancesEnabled
	if _, err := os.Stat(instanceDir); os.IsNotExist(err) {
		return fmt.Errorf("instances enabled directory doesn't exist: %s",
			instanceDir)
	}

	appList, err := util.CollectAppList(cmdCtx.Cli.ConfigDir,
		instanceDir, false)
	if err != nil {
		return fmt.Errorf("can't collect an application list: %s", err)
	}

	if len(appList) == 0 {
		log.Info("there are no enabled applications")
	}

	fmt.Println("List of enabled applications:")
	fmt.Printf("instances enabled directory: %s\n", instanceDir)

	for _, app := range appList {
		appLocation := strings.TrimPrefix(app.Location, instanceDir+string(os.PathSeparator))
		if !strings.HasSuffix(appLocation, ".lua") {
			appLocation = appLocation + string(os.PathSeparator)
		}
		log.Infof("%s (%s)", color.GreenString(strings.TrimSuffix(app.Name, ".lua")),
			appLocation)
		instances, _ := running.CollectInstances(app.Name, instanceDir)
		for _, inst := range instances {
			fullInstanceName := running.GetAppInstanceName(inst)
			if fullInstanceName != app.Name {
				fmt.Printf("	%s (%s)\n",
					color.YellowString(strings.TrimPrefix(fullInstanceName, app.Name+":")),
					strings.TrimPrefix(inst.InstanceScript, app.Location+string(os.PathSeparator)))
			}
		}
	}

	return nil
}
