package cluster_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tlsdialer"
	"github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

func TestNewTarantoolCollectors_Collect_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Collector cluster.DataCollector
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
		Publisher cluster.DataPublisher
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
		Publisher cluster.DataPublisher
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

func TestMakeConnectOptsFromUriOpts(t *testing.T) {
	type Expected struct {
		dialer tarantool.Dialer
		opts   tarantool.Opts
	}
	cases := []struct {
		Name     string
		UriOpts  connect.UriOpts
		Expected Expected
	}{
		{
			Name:    "empty",
			UriOpts: connect.UriOpts{},
			Expected: Expected{
				tarantool.NetDialer{
					Address: "tcp://",
				},
				tarantool.Opts{},
			},
		},
		{
			Name: "ignored",
			UriOpts: connect.UriOpts{
				Endpoint: "localhost:3013",
				Prefix:   "foo",
				Params: map[string]string{
					"key":  "bar",
					"name": "zoo",
				},
				CaPath:         "ca_path",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        673,
			},
			Expected: Expected{
				tarantool.NetDialer{
					Address: "tcp://",
				},
				tarantool.Opts{
					Timeout: 673,
				},
			},
		},
		{
			Name: "full",
			UriOpts: connect.UriOpts{
				Endpoint: "scheme://foo",
				Host:     "foo",
				Prefix:   "prefix",
				Params: map[string]string{
					"key":  "key",
					"name": "instance",
				},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				Ciphers:        "foo:bar:ciphers",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        2 * time.Second,
			},
			Expected: Expected{
				tlsdialer.OpenSSLDialer{
					Address:     "tcp://foo",
					User:        "username",
					Password:    "password",
					SslKeyFile:  "key_file",
					SslCertFile: "cert_file",
					SslCaFile:   "ca_file",
					SslCiphers:  "foo:bar:ciphers",
				},
				tarantool.Opts{
					Timeout: 2 * time.Second,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			dialer, opts := cluster.MakeConnectOptsFromUriOpts(tc.UriOpts)

			assert.Equal(t, tc.Expected.dialer, dialer)
			assert.Equal(t, tc.Expected.opts, opts)
		})
	}
}
