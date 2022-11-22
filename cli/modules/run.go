package modules

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
)

// InternalFunc is a type of function that implements
// the external behavior of the module.
type InternalFunc func(*cmdcontext.CmdCtx, []string) error

// RunCmd launches the required module.
// It collects a list of available modules, and based on
// flags and information about the modules, it launches
// the desired module.
//
// If an external module is defined and the -I flag is not
// specified, the external module will be launched.
// In any other case, internal.
//
// If the external module returns an error code,
// then tt exit with this code.
func RunCmd(cmdCtx *cmdcontext.CmdCtx, cmdName string, modulesInfo *ModulesInfo,
	internal InternalFunc, args []string) error {
	info, found := (*modulesInfo)[cmdName]
	if !found {
		return fmt.Errorf("module with specified name %s isn't found", cmdName)
	}

	if info.IsInternal || cmdCtx.Cli.ForceInternal {
		return internal(cmdCtx, args)
	}

	if rc := RunExec(info.ExternalPath, args); rc != 0 {
		os.Exit(rc)
	}

	return nil
}

// GetDefaultCmdArgs returns all arguments from the command line
// to external module that come after the command name.
func GetDefaultCmdArgs(cmdName string) []string {
	cmdNameIndexInArgs := util.Find(os.Args, cmdName)
	return os.Args[cmdNameIndexInArgs+1:]
}

// RunExec exec command with the supplied arguments.
// returns an error code from exec command.
func RunExec(command string, args []string) int {
	cmd := exec.Command(command, args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}

		log.Errorf("failed to exec external module: %s", err)
		return 1
	}

	return 0
}

// GetExternalModuleHelp calls external module with
// the --help flag and returns an output.
func GetExternalModuleHelp(module string) (string, error) {
	out, err := exec.Command(module, "--help").Output()
	return string(out), err
}

// GetExternalModuleDescription calls external module with
// the --help flag and returns an output.
func GetExternalModuleDescription(module string) (string, error) {
	out, err := exec.Command(module, "--description").Output()
	return string(out), err
}
