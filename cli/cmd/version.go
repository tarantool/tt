package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/version"
)

var (
	showShort   bool
	needCommit  bool
	showModules bool

	// modules are the modules the running distribution of tt is built from,
	// as the table of the core names them. Boot sets them.
	modules []version.Module

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
		RunE:  internalRunE(internalVersionModule),
	}

	versionCmd.Flags().BoolVar(&showShort, "short", false, "Show version in short format")
	versionCmd.Flags().BoolVar(&needCommit, "commit", false, "Show commit")
	versionCmd.Flags().BoolVar(&showModules, "modules", false,
		"Show the modules tt is built from and their versions")
	versionCmd.MarkFlagsMutuallyExclusive("short", "modules")
	versionCmd.MarkFlagsMutuallyExclusive("commit", "modules")

	return versionCmd
}

// internalVersionModule is a default (internal) version module function.
//
// In full a distribution with a version of its own shows the version of the
// tt core it is built on under its own; --modules adds the modules of the
// table of the core with the versions Go recorded for their Go modules.
func internalVersionModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var out strings.Builder

	out.WriteString(flavour.GetVersion(showShort, needCommit))
	out.WriteString("\n")

	if !showShort && !needCommit {
		if core, ok := flavour.CoreVersion(); ok {
			out.WriteString(core)
			out.WriteString("\n")
		}
	}

	if showModules {
		out.WriteString(version.FormatModules(version.ModuleVersions(modules)))
	}

	_, _ = fmt.Fprint(os.Stdout, out.String())

	return nil
}

// coreVersion returns the short version of the tt core: the version
// [platform].tt constraints are checked against and the one written to the
// files tt generates. It stays the core's whatever distribution runs it.
func coreVersion() string {
	return version.GetVersion(true, false)
}
