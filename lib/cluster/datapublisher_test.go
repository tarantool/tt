package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/lib/cluster"
)

func TestFactory_NewFilePublisher_integrity_not_supported(t *testing.T) {
	factory := cluster.NewFactory(
		cluster.WithIntegrity(cluster.IntegrityOptions{}),
	)
	publisher, err := factory.NewFilePublisher("any")

	assert.Nil(t, publisher)
	assert.EqualError(t, err,
		"publishing into a file with integrity data is not supported")
}
