// FIXME: Create new tests https://github.com/tarantool/tt/issues/1039

package modules

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

func getTestRootCmd() *cobra.Command {
	testRootCmd := &cobra.Command{
		Use:   "root",
		Short: "root cmd",

		PersistentPreRun: func(cmd *cobra.Command, args []string) {},

		Run: func(cmd *cobra.Command, args []string) {},
	}

	var testCmd = &cobra.Command{
		Use:   "testCmd",
		Short: "test cmd",
	}

	var levelCmd1 = &cobra.Command{
		Use:   "levelCmd1",
		Short: "level 1",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	var levelCmd2 = &cobra.Command{
		Use:   "levelCmd2",
		Short: "level 2",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	testSubCommands := []*cobra.Command{
		levelCmd1,
		levelCmd2,
	}

	for _, cmd := range testSubCommands {
		testCmd.AddCommand(cmd)
	}

	testRootCmd.AddCommand(testCmd)

	return testRootCmd
}

func TestModulesInfo(t *testing.T) {
	cliOpts := config.CliOpts{Env: &config.TtEnvOpts{BinDir: "./testdata/bin_dir"}}

	var cmdCtx cmdcontext.CmdCtx
	testRootCmd := getTestRootCmd()
	modulesInfo, err := GetModulesInfo(&cmdCtx, testRootCmd, &cliOpts)
	require.Nil(t, err)

	keys := make([]string, 0, len(modulesInfo))
	for key := range modulesInfo {
		keys = append(keys, key)
	}

	expectedKeys := []string{"root testCmd", "root testCmd levelCmd1", "root testCmd levelCmd2"}
	require.ElementsMatch(t, expectedKeys, keys)
}
