package cluster

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
)

func TestNewTCSStorageMutualTLS(t *testing.T) {
	for _, passwordFile := range []bool{false, true} {
		cfg := gsconnect.Config{
			Endpoints:   []string{"127.0.0.1:0", "https://" + startTLSIPROTO(t, true)},
			DialTimeout: time.Second,
			SSL: gsconnect.SSLConfig{
				Enable: true, KeyFile: "testdata/tls/localhost.enc.key",
				CertFile: "testdata/tls/localhost.crt", CaFile: "testdata/tls/ca.crt",
			},
		}
		if passwordFile {
			cfg.SSL.PasswordFile = "testdata/tls/passwords"
		} else {
			cfg.SSL.Password = "secret"
		}

		stor, cleanup, err := NewTCSStorage(t.Context(), cfg)
		require.NoError(t, err)
		t.Cleanup(cleanup)
		require.NotNil(t, stor)
	}

	cfg := gsconnect.Config{
		Endpoints: []string{startTLSIPROTO(t, true)}, DialTimeout: time.Second,
		SSL: gsconnect.SSLConfig{
			Enable: true, KeyFile: "testdata/tls/localhost.enc.key",
			CertFile: "testdata/tls/localhost.crt", CaFile: "testdata/tls/ca.crt",
			Password: "wrong",
		},
	}
	stor, cleanup, err := NewTCSStorage(t.Context(), cfg)
	require.Error(t, err)
	assert.Nil(t, stor)
	assert.Nil(t, cleanup)
}

func TestNewTCSStoragePlainOverridesSSLFiles(t *testing.T) {
	cfg := gsconnect.Config{
		Endpoints: []string{startIPROTO(t, nil)},
		SSL: gsconnect.SSLConfig{
			Enable: false, KeyFile: "missing.key", CertFile: "missing.crt", CaFile: "missing.ca",
			Password: "unused", PasswordFile: "missing-passwords",
		},
	}
	stor, cleanup, err := NewTCSStorage(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.NotNil(t, stor)

	// URI connections infer TLS before handing the authoritative bool to the
	// adapter, so existing SSL parameters on HTTP URIs still enable encryption.
	opts, err := sdkconnect.CreateURIOpts("http://localhost:3301?ssl_ca_file=ca.pem")
	require.NoError(t, err)
	assert.True(t, getTarantoolCfg(ConnectOpts{}, opts).SSL.Enable)
}

func TestNewTCSStorageContextAndTimeout(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	done := make(chan struct{})

	go func() {
		defer close(done)

		conn, err := listener.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()

			_, _ = io.Copy(io.Discard, conn)
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()

	stor, cleanup, err := NewTCSStorage(ctx, gsconnect.Config{
		Endpoints: []string{listener.Addr().String()}, DialTimeout: time.Minute,
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, stor)
	assert.Nil(t, cleanup)
	<-done

	stor, cleanup, err = NewTCSStorage(t.Context(), gsconnect.Config{
		Endpoints: []string{listener.Addr().String()}, DialTimeout: -time.Second,
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, stor)
	assert.Nil(t, cleanup)
}
