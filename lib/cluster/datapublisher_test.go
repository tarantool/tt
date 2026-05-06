package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/lib/cluster"
)

func TestIntegrityDataPublisherFactory_NewFile(t *testing.T) {
	factory := cluster.NewDataPublisherFactory(
		cluster.WithIntegrity(cluster.IntegrityOptions{}),
	)
	publisher, err := factory.NewFile("any")

	assert.Nil(t, publisher)
	assert.EqualError(t, err,
		"publishing into a file with integrity data is not supported")
}
