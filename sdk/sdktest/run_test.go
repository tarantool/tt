package sdktest_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// twoMounts is a module with a command at the root and one in a group it
// does not provide itself.
func twoMounts(services sdk.Services) []sdk.Mount {
	services.Log().Info("constructing")

	hello := &cobra.Command{
		Use:  "hello <name>",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(services.Streams().IO().Out, "hello, %s\n", args[0])

			return err
		},
	}

	var force bool

	status := &cobra.Command{
		Use: "status",
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := services.Project().Dir()
			if err != nil {
				return err
			}

			services.Log().Info("status", "dir", dir, "force", force)

			_, err = fmt.Fprintln(services.Streams().IO().Out, "ok")

			return err
		},
	}
	status.Flags().BoolVar(&force, "force", false, "force")

	// The nested mount comes first: the order of mounts does not matter.
	return []sdk.Mount{
		{Path: "replicaset vshard", Cmd: status},
		{Path: "", Cmd: hello},
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("a root command", func(t *testing.T) {
		t.Parallel()

		got := sdktest.Run(t, twoMounts, "hello", "world")

		require.NoError(t, got.Err)
		assert.Equal(t, "hello, world\n", got.Stdout)
		assert.Empty(t, got.Stderr)
		assert.Equal(t, sdk.ExitOK, got.ExitCode())
		require.Len(t, got.Records, 1, "the constructor logs once")
		assert.Equal(t, "constructing", got.Records[0].Message)
	})

	t.Run("a command in created groups", func(t *testing.T) {
		t.Parallel()

		got := sdktest.Run(t, twoMounts, "replicaset", "vshard", "status", "--force")

		require.NoError(t, got.Err)
		assert.Equal(t, "ok\n", got.Stdout)
		require.Len(t, got.Records, 2)
		assert.Equal(t, true, got.Records[1].Map()["force"])
		assert.Equal(t, "test", got.Records[1].Map()["module"])
	})

	t.Run("a created group prints its help", func(t *testing.T) {
		t.Parallel()

		got := sdktest.Run(t, twoMounts, "replicaset")

		require.NoError(t, got.Err)
		assert.Contains(t, got.Stdout, "tt replicaset [command]")
		assert.Contains(t, got.Stdout, "vshard")
	})

	t.Run("an argument error surfaces", func(t *testing.T) {
		t.Parallel()

		got := sdktest.Run(t, twoMounts, "hello")

		require.Error(t, got.Err)
		assert.Contains(t, got.Err.Error(), "accepts 1 arg(s)")
		assert.Empty(t, got.Stderr, "reporting the error is the core's job")
	})
}

func TestRunProvidedGroup(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, func(sdk.Services) []sdk.Mount {
		// The command is listed before the group it goes under.
		return []sdk.Mount{
			{Path: "cluster", Cmd: &cobra.Command{Use: "show", Run: func(*cobra.Command, []string) {}}},
			{Path: "", Cmd: &cobra.Command{Use: "cluster", Short: "Manage the cluster"}},
		}
	}, "--help")

	require.NoError(t, got.Err)
	assert.Contains(t, got.Stdout, "Manage the cluster", "the provided group is the one hung")
}

func TestRunError(t *testing.T) {
	t.Parallel()

	errPartial := errors.New("two of three instances")

	got := sdktest.Run(t, func(sdk.Services) []sdk.Mount {
		return []sdk.Mount{{Path: "", Cmd: &cobra.Command{
			Use: "restart",
			RunE: func(*cobra.Command, []string) error {
				return sdk.WithCode(sdk.ExitPartial, errPartial)
			},
		}}}
	}, "restart")

	require.ErrorIs(t, got.Err, errPartial)
	assert.Equal(t, sdk.ExitPartial, got.ExitCode())
}

func TestRunWithOptions(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithAnswers(false))

	got := services.Run(func(services sdk.Services) []sdk.Mount {
		return []sdk.Mount{{Path: "", Cmd: &cobra.Command{
			Use: "stop",
			RunE: func(*cobra.Command, []string) error {
				proceed, err := services.Confirm("Stop everything?", true)
				if err != nil {
					return err
				}

				if !proceed {
					return sdk.Usagef("declined")
				}

				return nil
			},
		}}}
	}, "stop")

	var usage *sdk.UsageError

	require.ErrorAs(t, got.Err, &usage)
	assert.Equal(t, "Stop everything? [y/n]: ", got.Stderr)
}

func TestRunConstructorUsesServicesTooEarly(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, `module "test" used Services.Tarantool before tt was configured; `+
		"use it in a command's hooks, not in the constructor", func() {
		sdktest.Run(t, func(services sdk.Services) []sdk.Mount {
			_, _ = services.Tarantool()

			return nil
		})
	})
}

func TestRunRefusesBadMounts(t *testing.T) {
	t.Parallel()

	parented := &cobra.Command{Use: "child"}
	(&cobra.Command{Use: "parent"}).AddCommand(parented)

	cases := []struct {
		name   string
		mounts []sdk.Mount
		want   string
	}{
		{
			name:   "no command",
			mounts: []sdk.Mount{{Path: "group", Cmd: nil}},
			want:   `mount under "group" has no command`,
		},
		{
			name:   "a command with a parent",
			mounts: []sdk.Mount{{Path: "", Cmd: parented}},
			want:   `command "child" already has a parent "parent"`,
		},
		{
			name: "duplicate names",
			mounts: []sdk.Mount{
				{Path: "group", Cmd: &cobra.Command{Use: "op"}},
				{Path: "group", Cmd: &cobra.Command{Use: "op"}},
			},
			want: `command "tt group op" clashes with "tt group op"`,
		},
		{
			name: "a name clashing with an alias",
			mounts: []sdk.Mount{
				{Path: "", Cmd: &cobra.Command{Use: "status", Aliases: []string{"st"}}},
				{Path: "", Cmd: &cobra.Command{Use: "st"}},
			},
			want: `command "tt st" clashes with "tt status"`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			message := fatal(t, func(tb testing.TB) {
				tb.Helper()
				sdktest.Run(tb, func(sdk.Services) []sdk.Mount { return testCase.mounts })
			})

			assert.Contains(t, message, testCase.want)
		})
	}
}
