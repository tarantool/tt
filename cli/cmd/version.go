package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/version"
)

var (
	showShort  bool
	needCommit bool

	// flavour is how the running distribution of tt presents itself: its
	// name in the help and in tt version, and its version there and in
	// internal errors. Boot sets it.
	flavour version.Flavour
)

// NewVersionCmd creates a new version command.
func NewVersionCmd() *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Show " + flavour.Name() + " version information",
		RunE:  RunModuleFuncE(internalVersionModule),
	}

	versionCmd.Flags().BoolVar(&showShort, "short", false, "Show version in short format")
	versionCmd.Flags().BoolVar(&needCommit, "commit", false, "Show commit")

	return versionCmd
}

// internalVersionModule is a default (internal) version module function.
func internalVersionModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	_, _ = fmt.Fprintln(os.Stdout, flavour.GetVersion(showShort, needCommit))
	return nil
}

// coreVersion returns the short version of the tt core: the version
// [platform].tt constraints are checked against and the one written to the
// files tt generates. It stays the core's whatever distribution runs it.
func coreVersion() string {
	return version.GetVersion(true, false)
}
