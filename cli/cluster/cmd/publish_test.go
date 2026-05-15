package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	libcluster "github.com/tarantool/tt/lib/cluster"
)

// TestPublishCluster_FileWithIntegrity_Errors verifies that PublishCluster
// returns an error when the publisher factory does not support file publishing
// with integrity data (e.g. IntegrityDataPublisherFactory).
func TestPublishCluster_FileWithIntegrity_Errors(t *testing.T) {
	src := []byte(`groups:
  g:
    replicasets:
      r:
        instances:
          inst: {}
`)

	ctx := PublishCtx{
		Force: true, // skip validation
		Publishers: libcluster.NewDataPublisherFactory(
			libcluster.WithIntegrity(libcluster.IntegrityOptions{}),
		),
		Collectors: libcluster.NewDataCollectorFactory(),
		Src:        src,
	}

	// Publish without instance — goes through the "easy" path: publisher.Publish.
	// IntegrityDataPublisherFactory.NewFile returns an error, so this must fail.
	err := PublishCluster(ctx, "any/path.yaml", "")
	require.ErrorContains(t, err, "publishing into a file with integrity data is not supported")
}
