package sdk_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3"
)

// TestDependencyMoveString pins the lines tt package and the modules report
// the moves of a lock with.
func TestDependencyMoveString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a 1.0.0-1", sdk.DependencyMove{Name: "a", From: "", To: "1.0.0-1"}.String())
	assert.Equal(t, "a 1.0.0-1 -> (dropped)",
		sdk.DependencyMove{Name: "a", From: "1.0.0-1", To: ""}.String())
	assert.Equal(t, "a 1.0.0-1 -> 1.1.0-1",
		sdk.DependencyMove{Name: "a", From: "1.0.0-1", To: "1.1.0-1"}.String())
}

// TestAddJobsFlag checks -j, --jobs: its names and help, a number of jobs
// set by either, and a negative one or no number refused, the value kept.
func TestAddJobsFlag(t *testing.T) {
	t.Parallel()

	newCommand := func(jobs *int) *cobra.Command {
		cmd := &cobra.Command{Use: "build"}
		sdk.AddJobsFlag(cmd, jobs)

		return cmd
	}

	var jobs int

	flag := newCommand(&jobs).Flags().Lookup(sdk.JobsFlag)
	require.NotNil(t, flag)
	assert.Equal(t, "j", flag.Shorthand)
	assert.Equal(t, "0", flag.DefValue)
	assert.Equal(t, "int", flag.Value.Type())
	assert.Equal(t, sdk.JobsFlagUsage, flag.Usage)

	for args, want := range map[string]int{
		"-j 4": 4, "-j4": 4, "--jobs 2": 2, "--jobs=16": 16, "-j 0": 0,
	} {
		jobs = 9

		cmd := newCommand(&jobs)

		require.NoError(t, cmd.Flags().Parse(strings.Fields(args)), args)
		assert.Equal(t, want, jobs, args)
		assert.Equal(t, strconv.Itoa(want), cmd.Flags().Lookup(sdk.JobsFlag).Value.String())
	}

	for _, args := range []string{"-j -1", "--jobs=-3", "-j many", "--jobs 1.5", "--jobs="} {
		jobs = 7

		err := newCommand(&jobs).Flags().Parse(strings.Fields(args))
		require.Error(t, err, args)
		assert.Contains(t, err.Error(), "not a number of jobs", args)
		assert.Equal(t, 7, jobs, "%s: a bad number keeps the value", args)
	}
}
