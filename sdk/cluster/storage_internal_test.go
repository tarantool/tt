package cluster

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	etcdtest "github.com/tarantool/go-storage/v2/test_helpers/etcd"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
	"github.com/vmihailenco/msgpack/v5"
	"go.etcd.io/etcd/client/pkg/v3/transport"
)

func unrelatedCA(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	cert := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Unrelated test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: der,
	}), 0o600))

	return path
}

func TestEtcdHTTPS(t *testing.T) {
	server := etcdtest.New(t, etcdtest.ClusterConfig{
		ClientTLS: &transport.TLSInfo{
			CertFile: "testdata/tls/localhost.crt",
			KeyFile:  "testdata/tls/localhost.key",
		},
	})
	endpoint := server.EndpointsGRPC()[0]
	wrongCA := unrelatedCA(t)
	caDir := t.TempDir()
	ca, err := os.ReadFile("testdata/tls/ca.crt")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(caDir, "ca.pem"), ca, 0o600))

	cases := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{name: "no SSL parameters"},
		{name: "trusted CA file", query: "ssl_ca_file=testdata/tls/ca.crt"},
		{name: "trusted CA directory", query: "ssl_ca_path=" + caDir},
		{name: "untrusted CA file", query: "ssl_ca_file=" + wrongCA, wantErr: true},
		{name: "empty CA directory", query: "ssl_ca_path=" + t.TempDir(), wantErr: true},
		{
			name:  "explicit skip host verification",
			query: "ssl_ca_file=" + wrongCA + "&verify_host=false",
		},
		{
			name:  "explicit skip peer verification",
			query: "ssl_ca_file=" + wrongCA + "&verify_peer=false",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			opts, err := sdkconnect.CreateURIOpts(endpoint + "?timeout=1&" + testCase.query)
			require.NoError(t, err)

			client, err := connectEtcdClient(getEtcdCfg(ConnectOpts{}, opts))
			if testCase.wantErr {
				require.Error(t, err)
				require.Nil(t, client)

				return
			}

			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, client.Close()) })

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			_, err = client.Get(ctx, "config")
			require.NoError(t, err)
		})
	}

	opts, err := sdkconnect.CreateURIOpts(endpoint)
	require.NoError(t, err)

	_, cleanup, backend, err := NewStorageConnection(ConnectOpts{}, opts)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	assert.Equal(t, etcdStorageType, backend)
}

func TestEtcdPlainEndpoints(t *testing.T) {
	server := etcdtest.New(t, etcdtest.ClusterConfig{})
	endpoint := server.EndpointsGRPC()[0]

	for _, endpoint := range []string{endpoint, strings.TrimPrefix(endpoint, "http://")} {
		t.Run(endpoint, func(t *testing.T) {
			cfg := gsconnect.Config{
				Endpoints:   []string{endpoint},
				DialTimeout: time.Second,
				SSL:         gsconnect.SSLConfig{VerifyHost: true, VerifyPeer: true},
			}
			assert.False(t, needsEtcdTLSConfig(cfg.SSL))

			client, err := connectEtcdClient(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, client.Close()) })
		})
	}

	t.Run("HTTPS rejects plaintext server", func(t *testing.T) {
		opts, err := sdkconnect.CreateURIOpts(strings.Replace(endpoint, "http://", "https://", 1) +
			"?timeout=1")
		require.NoError(t, err)

		client, err := connectEtcdClient(getEtcdCfg(ConnectOpts{}, opts))
		require.Error(t, err)
		assert.Nil(t, client)
	})
}

// startTLSIPROTO serves just the greeting, identification and empty schema that
// go-tarantool needs to connect. gRPC probes fail, exercising the TCS fallback.
func startTLSIPROTO(t *testing.T, mutualTLS ...bool) string {
	t.Helper()

	cert, err := tls.LoadX509KeyPair("testdata/tls/localhost.crt", "testdata/tls/localhost.key")
	require.NoError(t, err)

	config := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	if len(mutualTLS) > 0 && mutualTLS[0] {
		ca, err := os.ReadFile("testdata/tls/ca.crt")
		require.NoError(t, err)

		config.ClientCAs = x509.NewCertPool()
		require.True(t, config.ClientCAs.AppendCertsFromPEM(ca))

		config.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return startIPROTO(t, config)
}

func startIPROTO(t *testing.T, config *tls.Config) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	if config != nil {
		listener = tls.NewListener(listener, config)
	}

	var workers sync.WaitGroup

	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			workers.Go(func() {
				defer func() { _ = conn.Close() }()

				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				serveTLSIPROTO(conn)
			})
		}
	}()

	t.Cleanup(func() {
		assert.NoError(t, listener.Close())
		<-done
		workers.Wait()
	})

	return listener.Addr().String()
}

func serveTLSIPROTO(conn net.Conn) {
	salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	greeting := fmt.Sprintf("%-63s\n%-63s\n", "Tarantool 2.10.0 (Binary)", salt)

	_, err := io.WriteString(conn, greeting)
	if err != nil {
		return
	}

	for {
		var length [5]byte

		_, err := io.ReadFull(conn, length[:])
		if err != nil || length[0] != 0xce {
			return
		}

		size := binary.BigEndian.Uint32(length[1:])
		if size > 4096 {
			return
		}

		packet := make([]byte, size)

		_, err = io.ReadFull(conn, packet)
		if err != nil {
			return
		}

		var header map[uint64]uint64

		err = msgpack.NewDecoder(bytes.NewReader(packet)).Decode(&header)
		if err != nil {
			return
		}

		var response bytes.Buffer

		encoder := msgpack.NewEncoder(&response)

		err = encoder.Encode(map[uint64]uint64{0: 0, 1: header[1]})
		if err != nil {
			return
		}

		body := map[uint64]any{0x30: []any{}}
		if header[0] == 0x49 { // IPROTO_ID.
			body = map[uint64]any{0x54: uint64(6), 0x55: []uint64{}}
		}

		err = encoder.Encode(body)
		if err != nil {
			return
		}

		// Fixed response maps are always smaller than a uint32 packet length.
		binary.BigEndian.PutUint32(length[1:], uint32(response.Len())) //nolint:gosec

		_, err = conn.Write(append(length[:], response.Bytes()...))
		if err != nil {
			return
		}
	}
}

func TestTarantoolHTTPSFallback(t *testing.T) {
	endpoint := startTLSIPROTO(t)

	opts, err := sdkconnect.CreateURIOpts("https://" + endpoint +
		"?timeout=1&ssl_ca_file=testdata/tls/ca.crt")
	require.NoError(t, err)

	_, cleanup, backend, err := NewStorageConnection(ConnectOpts{}, opts)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	assert.Equal(t, tcsStorageType, backend)

	opts, err = sdkconnect.CreateURIOpts("https://" + endpoint +
		"?timeout=1&ssl_ca_file=" + unrelatedCA(t))
	require.NoError(t, err)

	conn, err := connectTarantoolContext(t.Context(), getTarantoolCfg(ConnectOpts{}, opts))
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorContains(t, err, "certificate")
}

// A subprocess isolates x509's cached system pool from the parent tests. The
// no-CA connection uses the native backend's system trust store as before.
func TestTarantoolHTTPSSystemCA(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("system certificate pools on this platform do not read SSL_CERT_FILE")
	}

	if endpoint := os.Getenv("TT_TEST_TLS_ENDPOINT"); endpoint != "" {
		opts, err := sdkconnect.CreateURIOpts("https://" + endpoint + "?timeout=1")
		require.NoError(t, err)

		_, cleanup, backend, err := NewStorageConnection(ConnectOpts{}, opts)
		require.NoError(t, err)
		t.Cleanup(cleanup)
		assert.Equal(t, tcsStorageType, backend)

		return
	}

	endpoint := startTLSIPROTO(t)
	caFile, err := filepath.Abs("testdata/tls/ca.crt")
	require.NoError(t, err)

	executable, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestTarantoolHTTPSSystemCA$")

	cmd.Env = append(os.Environ(), "TT_TEST_TLS_ENDPOINT="+endpoint,
		"SSL_CERT_FILE="+caFile, "SSL_CERT_DIR="+t.TempDir())

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
