package cmd

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	// runEval contains "-e" flag content.
	runEval string
	// runLib contains "-l" flag content.
	runLib string
	// runInteractive contains "-I" flag content.
	runInteractive bool
	// runStdin contains "-" flag content.
	runStdin string
	// runVersion contains "-v" flag content.
	runVersion bool
	// runArgs contains command args.
	runArgs []string
)

func newRunOpts(cmdCtx cmdcontext.CmdCtx) *running.RunOpts {
	return &running.RunOpts{
		CmdCtx: cmdCtx,
		RunFlags: running.RunFlags{
			RunEval:        runEval,
			RunLib:         runLib,
			RunInteractive: runInteractive,
			RunStdin:       runStdin,
			RunVersion:     runVersion,
			RunArgs:        runArgs,
		},
	}
}

// NewRunCmd creates run command.
func NewRunCmd() *cobra.Command {
	var runCmd = &cobra.Command{
		Use:   "run [APPLICATION_NAME]",
		Short: "Run tarantool instance",
		Long: "Run tarantool instance\n" +
			"Flags processed within the application\n" +
			"are passed after: '--'",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalRunModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}
	runCmd.Flags().StringVarP(&runEval, "evaluate", "e", "", "execute string 'EXPR'")
	runCmd.Flags().StringVarP(&runLib, "library", "l", "", "require library 'NAME'")
	runCmd.Flags().BoolVarP(&runInteractive, "interactive", "i", false,
		"enter interactive mode after executing 'SCRIPT'")
	runCmd.Flags().BoolVarP(&runVersion, "version", "v", false, "print used tarantool version")

	return runCmd
}

// internalRunModule is a default run module.
func internalRunModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	runOpts := newRunOpts(*cmdCtx)
	scriptPath := ""
	startIndex := 0
	if len(args) > 0 {
		if strings.HasSuffix(args[0], ".lua") {
			scriptPath = args[0]
			if _, err := os.Stat(scriptPath); err != nil {
				return fmt.Errorf("there was some problem locating script: %s", err)
			}
			startIndex = 1
		} else {
			return fmt.Errorf("specify script : %s", args[0])
		}
	}
	if len(args) > 0 {
		// If '-' flag is specified, then read stdin.
		if args[0] == "-" {
			// Code below reads input when run is called
			// with input through pipe e.g "test.lua | ./tt run -".
			if !terminal.IsTerminal(syscall.Stdin) {
				cmdByte, err := ioutil.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				runStdin = string(cmdByte)
				if len(args) > 1 {
					for i := 1; i < len(args); i++ {
						runArgs = append(runArgs, args[i])
					}
				}
			} else {
				runStdin = ""
				for i := 1; i < len(args); i++ {
					runStdin += args[i]
				}
			}
		} else {
			if len(args) > 0 {
				for i := startIndex; i < len(args); i++ {
					runArgs = append(runArgs, args[i])
				}
				runOpts.RunFlags.RunArgs = runArgs
			}
		}
	}

	if err := running.Run(runOpts, scriptPath); err != nil {
		return err
	}

	return nil
}
