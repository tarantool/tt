package sdktest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// errGaveUp is what the exiting command ends with.
var errGaveUp = errors.New("gave up")

// clusterYAML is a cluster with a global value and an instance that
// overrides it.
const clusterYAML = `
roles_cfg:
  aeon.grpc:
    advertise:
      uri: global:50051
groups:
  g:
    replicasets:
      r:
        instances:
          i1: {}
          i2:
            roles_cfg:
              aeon.grpc:
                advertise:
                  uri: i2:50051
`

// advertise is a module whose command prints the aeon advertise URI of an
// instance, named as <source> <instance>, and whose "give-up" command ends
// through Exit with the code its argument asks for.
func advertise(services sdk.Services) []sdk.Mount {
	show := &cobra.Command{
		Use:  "advertise <source> <instance>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := sdk.ParseClusterSource(args[0], sdk.Credentials{})

			cfg, err := services.ClusterConfig(cmd.Context(), src)
			if err != nil {
				return err
			}

			instance, err := sdk.InstanceConfig(cfg.Config, args[1])
			if err != nil {
				return err
			}

			var uri string

			_, err = instance.Get(goconfig.NewKeyPath("roles_cfg/aeon.grpc/advertise/uri"), &uri)
			if err != nil {
				return fmt.Errorf("advertise: %w", err)
			}

			_, err = fmt.Fprintln(services.Streams().IO().Out, uri)

			return err
		},
	}

	giveUp := &cobra.Command{
		Use: "give-up",
		RunE: func(_ *cobra.Command, _ []string) error {
			services.Exit(sdk.WithCode(sdk.ExitPartial, errGaveUp))

			return errors.New("unreachable: Exit returned")
		},
	}

	return []sdk.Mount{{Path: "", Cmd: show}, {Path: "", Cmd: giveUp}}
}

// TestWithClusterConfig checks that a module reads the configuration given
// for a source, with the hierarchy resolved, and that another source is not
// found.
func TestWithClusterConfig(t *testing.T) {
	t.Parallel()

	newServices := func(t *testing.T) *sdktest.Services {
		t.Helper()

		return sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), clusterYAML))
	}

	got := newServices(t).Run(advertise, "advertise", "app", "i2")
	require.NoError(t, got.Err)
	assert.Equal(t, "i2:50051\n", got.Stdout)

	got = newServices(t).Run(advertise, "advertise", "app", "i1")
	require.NoError(t, got.Err)
	assert.Equal(t, "global:50051\n", got.Stdout, "inherited from the global section")

	got = newServices(t).Run(advertise, "advertise", "app", "i3")
	require.ErrorIs(t, got.Err, sdk.ErrNotFound)
	require.EqualError(t, got.Err, `instance "i3" not found`)

	got = newServices(t).Run(advertise, "advertise", "other", "i1")
	require.ErrorIs(t, got.Err, sdk.ErrNotFound)
	assert.Contains(t, got.Err.Error(), `application "other"`)
	assert.False(t, got.Exited)
}

// TestWithClusterConfigDir checks the Dir ClusterConfig reports: the
// directory of a file source's file, "" for other sources, and what
// WithClusterConfigDir sets.
func TestWithClusterConfigDir(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	require.NoError(t, err)

	file := sdk.FileSource(filepath.Join("conf", "cluster.yaml"))
	app := sdk.AppSource("app")
	other := sdk.AppSource("other")
	storage := sdk.StorageSource("http://localhost:2379/prefix", sdk.Credentials{})

	services := sdktest.New(t,
		sdktest.WithClusterConfig(file, clusterYAML),
		sdktest.WithClusterConfig(app, clusterYAML),
		sdktest.WithClusterConfig(other, clusterYAML),
		sdktest.WithClusterConfig(storage, clusterYAML),
		sdktest.WithClusterConfigDir(app, "/srv/app"))
	services.Start()

	for src, want := range map[sdk.ClusterSource]string{
		file:    filepath.Join(cwd, "conf"),
		app:     "/srv/app",
		other:   "",
		storage: "",
	} {
		cfg, err := services.ClusterConfig(t.Context(), src)
		require.NoError(t, err, src.String())
		assert.Equal(t, want, cfg.Dir, src.String())
	}
}

// TestWithClusterConfigBadYAML checks that YAML that does not parse fails
// the test in New.
func TestWithClusterConfigBadYAML(t *testing.T) {
	t.Parallel()

	message := fatal(t, func(tb testing.TB) {
		tb.Helper()

		sdktest.New(tb, sdktest.WithClusterConfig(sdk.FileSource("c.yaml"), "a: [b"))
	})
	assert.Contains(t, message, `WithClusterConfig(file "c.yaml")`)
}

// TestExit checks that Exit ends the command, not the test, and that the
// result reports the error and its code.
func TestExit(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, advertise, "give-up")

	assert.True(t, got.Exited)
	require.ErrorIs(t, got.Err, errGaveUp)
	assert.Equal(t, sdk.ExitPartial, got.ExitCode())
}

// TestExitOutsideRun checks that Exit outside Run panics with a value that
// names the error.
func TestExitOutsideRun(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	defer func() {
		recovered := recover()

		err, ok := recovered.(error)
		require.True(t, ok, "Exit panics with an error: %v", recovered)
		assert.ErrorContains(t, err, "outside Services.Run: gave up")
	}()

	services.Exit(errGaveUp)
}
