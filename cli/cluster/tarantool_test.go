package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/integrity"
)

func TestNewTarantoolCollectors_Collect_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Collector integrity.DataCollector
	}{
		{"any", cluster.NewTarantoolAllCollector(nil, "", 0)},
		{"key", cluster.NewTarantoolKeyCollector(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Collector.Collect()
			})
		})
	}
}

func TestNewTarantoolDataPublishers(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher integrity.DataPublisher
	}{
		{"all", cluster.NewTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.NotNil(t, tc.Publisher)
		})
	}
}

func TestAllTarantoolDataPublisher_Publish_revision(t *testing.T) {
	publisher := cluster.NewTarantoolAllDataPublisher(nil, "", 0)
	err := publisher.Publish(1, []byte{})
	assert.EqualError(
		t, err, "failed to publish data into tarantool: target revision 1 is not supported")
}

func TestNewTarantoolDataPublishers_Publish_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher integrity.DataPublisher
	}{
		{"all", cluster.NewTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Publisher.Publish(0, []byte{})
			})
		})
	}
}
