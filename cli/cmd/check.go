package cmd

import (
	"fmt"
	"os/exec"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
)

// NewCheckCmd creates a new ckeck command.
func NewCheckCmd() *cobra.Command {
	var checkCmd = &cobra.Command{
		Use:   "check",
		Short: "Check an instance file for syntax errors",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalCheckModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return checkCmd
}

// internalCheckModule is a default (internal) ckeck module function.
func internalCheckModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	checkFun := `
	(function()
		local os = require('os')
		local instance_path = os.getenv('TT_CLI_INSTANCE')
		if instance_path == nil then
			print('Failed to get instance path from TT_CLI_INSTANCE env variable')
			return
		end
		local rv, err = loadfile(instance_path)
		if rv == nil then
			print(string.format("%s", debug.traceback()))
			print(string.format("Failed to check instance file '%s'", err))
			return
		end
		print(string.format("Syntax of file '%s' is OK", instance_path))
	end)()
	`

	cmd := exec.Command("tarantool", "-e", checkFun)
	stdout, err := cmd.Output()
	if err != nil {
		fmt.Println("Problem with exec lua check function code via tarantool -e")
		fmt.Println(err.Error())
		return nil
	}
	fmt.Println(string(stdout))
	return nil
}
