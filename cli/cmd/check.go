package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
)

// NewCheckCmd creates a new check command.
func NewCheckCmd() *cobra.Command {
	var checkCmd = setupTtModuleCmd(internalCheckModule, &cobra.Command{
		Use:   "check [<APPLICATION_NAME>]",
		Short: "Check an application file for syntax errors",
	})

	return checkCmd
}

// internalCheckModule is a default check module.
func internalCheckModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, true); err != nil {
		return err
	}

	// Сollect a list of instances with unique scripts.
	uniqueInst := []running.InstanceCtx{}
	for _, inst := range runningCtx.Instances {
		found := false
		for _, unique := range uniqueInst {
			if inst.InstanceScript == unique.InstanceScript {
				found = true
				break
			}
		}

		if !found {
			uniqueInst = append(uniqueInst, inst)
		}
	}

	for _, inst := range uniqueInst {
		if err := running.Check(cmdCtx, &inst); err != nil {
			return err
		}
		log.Infof("Result of check: syntax of file '%s' is OK", inst.InstanceScript)
	}

	return nil
}
