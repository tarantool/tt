package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	clustercmd "github.com/tarantool/tt/v3/cli/cluster/cmd"
)

// TestPublishApp_FileWithIntegrity_Errors verifies that PublishApp returns an
// error when the publisher factory does not support file publishing with
// integrity data (e.g. IntegrityDataPublisherFactory).
func TestPublishApp_FileWithIntegrity_Errors(t *testing.T) {
	src := []byte(`groups:
  g:
    replicasets:
      r:
        instances:
          inst: {}
`)

	ctx := clustercmd.PublishCtx{
		Force: true, // skip validation.
		Publishers: sdkcluster.NewFactory(
			sdkcluster.WithIntegrity(sdkcluster.IntegrityOptions{}),
		),
		Collectors: sdkcluster.NewFactory(),
		Src:        src,
	}

	// A config that points to no storage is published into the file itself.
	// IntegrityDataPublisherFactory.NewFile returns an error, so this must fail.
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, src, 0o600))

	err := clustercmd.PublishApp(ctx, path, "")
	require.ErrorContains(t, err, "publishing into a file with integrity data is not supported")
}
