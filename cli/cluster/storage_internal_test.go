package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"
	etcdtest "github.com/tarantool/go-storage/v2/test_helpers/etcd"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestReadEtcdEndpointsHTTPS(t *testing.T) {
	certDir := filepath.Join("..", "..", "sdk", "cluster", "testdata", "tls")
	caFile := filepath.Join(certDir, "ca.crt")
	server := etcdtest.New(t, etcdtest.ClusterConfig{
		ClientTLS: &transport.TLSInfo{
			CertFile: filepath.Join(certDir, "localhost.crt"),
			KeyFile:  filepath.Join(certDir, "localhost.key"),
		},
	})
	ca, err := os.ReadFile(caFile)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca))

	client, err := clientv3.New(clientv3.Config{
		Endpoints: server.EndpointsGRPC(),
		TLS:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err = client.Put(ctx, "/test/config/all", "foo: bar\n")
	require.NoError(t, err)

	cases := []struct {
		name      string
		endpoints string
		ssl       string
		wantErr   bool
	}{
		{name: "no SSL parameters"},
		{
			name: "mixed schemes use TLS for all endpoints",
			endpoints: fmt.Sprintf("%q, %q",
				strings.Replace(server.EndpointsGRPC()[0], "https://", "http://", 1),
				server.EndpointsGRPC()[0]),
		},
		{name: "no CA with explicit verification", ssl: "verify_peer: true\nverify_host: true"},
		{name: "custom CA verifies by default", ssl: fmt.Sprintf("ca_file: %q", caFile)},
		{
			name:    "untrusted CA directory rejects",
			ssl:     fmt.Sprintf("ca_path: %q", t.TempDir()),
			wantErr: true,
		},
		{
			name: "explicit verification skip",
			ssl:  fmt.Sprintf("ca_path: %q\nverify_peer: false\nverify_host: false", t.TempDir()),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sslBlock := "{}"
			if testCase.ssl != "" {
				sslBlock = "\n      " + strings.ReplaceAll(testCase.ssl, "\n", "\n      ")
			}

			endpoints := testCase.endpoints
			if endpoints == "" {
				endpoints = fmt.Sprintf("%q", server.EndpointsGRPC()[0])
			}

			cfg, err := BuildGoConfigFromBytes(t.Context(), fmt.Appendf(nil,
				"config:\n  etcd:\n    endpoints: [%s]\n    prefix: /test\n"+
					"    http: {request: {timeout: 1}}\n    ssl: %s",
				endpoints, sslBlock))
			require.NoError(t, err)

			ref, found, err := readEtcdRef(cfg)
			require.NoError(t, err)
			require.True(t, found)

			got, cleanup, err := readEtcd(t.Context(), ref, sdkcluster.NewFactory())
			if testCase.wantErr {
				require.Error(t, err)
				assert.Equal(t, goconfig.Config{}, got)
				assert.Nil(t, cleanup)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, cleanup)
			t.Cleanup(cleanup)
			require.NotEqual(t, goconfig.Config{}, got)

			value, err := cfgGetString(got, "foo")
			require.NoError(t, err)
			assert.Equal(t, "bar", value)
		})
	}
}
