package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/lib/cluster"
)

func TestNewGSTarantoolCollectors_Collect_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Collector cluster.DataCollector
	}{
		{"any", cluster.NewGSTarantoolAllCollector(nil, "", 0)},
		{"key", cluster.NewGSTarantoolKeyCollector(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Collector.Collect()
			})
		})
	}
}

func TestNewGSTarantoolDataPublishers(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewGSTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewGSTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.NotNil(t, tc.Publisher)
		})
	}
}

func TestAllGSTarantoolDataPublisher_Publish_revision(t *testing.T) {
	publisher := cluster.NewGSTarantoolAllDataPublisher(nil, "", 0)
	err := publisher.Publish(1, []byte{})
	assert.EqualError(
		t, err, "failed to publish data into tarantool: target revision 1 is not supported")
}

func TestGSTarantoolDataPublishers_Publish_data_nil(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewGSTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewGSTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := tc.Publisher.Publish(0, nil)

			assert.EqualError(t, err,
				"failed to publish data into tarantool: data does not exist")
		})
	}
}

func TestNewGSTarantoolDataPublishers_Publish_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewGSTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewGSTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Publisher.Publish(0, []byte{})
			})
		})
	}
}
