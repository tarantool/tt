package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

// TestJobsFlag: every command that builds the components of a project takes
// -j, --jobs, bound to the jobs of its build, and refuses a negative number
// before anything runs.
func TestJobsFlag(t *testing.T) {
	oldPackage, oldTest := packageJobs, testJobs

	t.Cleanup(func() { packageJobs, testJobs = oldPackage, oldTest })

	cases := []struct {
		name       string
		newCommand func() *cobra.Command
		jobs       *int
	}{
		{"package build", newPackageBuildCmd, &packageJobs},
		{"package pack", newPackagePackCmd, &packageJobs},
		{"test", NewTestCmd, &testJobs},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			*testCase.jobs = 0

			command := testCase.newCommand()

			flag := command.Flags().Lookup(sdk.JobsFlag)
			require.NotNil(t, flag)
			assert.Equal(t, "j", flag.Shorthand)
			assert.Equal(t, "0", flag.DefValue)

			require.NoError(t, command.Flags().Parse([]string{"-j", "3"}))
			assert.Equal(t, 3, *testCase.jobs)

			require.NoError(t, command.Flags().Parse([]string{"--jobs=0"}))
			assert.Equal(t, 0, *testCase.jobs)

			err := testCase.newCommand().Flags().Parse([]string{"-j", "-2"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not a number of jobs")
		})
	}
}

// TestJobsFlagNotOnFetch: tt package fetch builds no component, and has no
// -j.
func TestJobsFlagNotOnFetch(t *testing.T) {
	assert.Nil(t, newPackageFetchCmd().Flags().Lookup(sdk.JobsFlag))
}
