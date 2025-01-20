package connect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

func TestMakeEtcdOptsFromUriOpts(t *testing.T) {
	cases := []struct {
		Name     string
		UriOpts  UriOpts
		Expected libcluster.EtcdOpts
	}{
		{
			Name:     "empty",
			UriOpts:  UriOpts{},
			Expected: libcluster.EtcdOpts{},
		},
		{
			Name: "ignored",
			UriOpts: UriOpts{
				Host:     "foo",
				Prefix:   "foo",
				Key:      "bar",
				Instance: "zoo",
				Ciphers:  "foo:bar:ciphers",
			},
			Expected: libcluster.EtcdOpts{},
		},
		{
			Name: "skip_host_verify",
			UriOpts: UriOpts{
				SkipHostVerify: true,
			},
			Expected: libcluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "skip_peer_verify",
			UriOpts: UriOpts{
				SkipPeerVerify: true,
			},
			Expected: libcluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "full",
			UriOpts: UriOpts{
				Endpoint:       "foo",
				Host:           "host",
				Prefix:         "prefix",
				Key:            "key",
				Instance:       "instance",
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        2 * time.Second,
			},
			Expected: libcluster.EtcdOpts{
				Endpoints:      []string{"foo"},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				SkipHostVerify: true,
				Timeout:        2 * time.Second,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			etcdOpts := makeEtcdOptsFromUriOpts(tc.UriOpts)

			assert.Equal(t, tc.Expected, etcdOpts)
		})
	}
}
