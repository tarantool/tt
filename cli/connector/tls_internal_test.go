package connector

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/dial/v3"
	"github.com/vmihailenco/msgpack/v5"
)

func startConnectorTestServer(t *testing.T, serve func(int, net.Conn)) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var workers sync.WaitGroup

	done := make(chan struct{})

	go func() {
		defer close(done)

		for index := 0; ; index++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			workers.Go(func() {
				defer func() { _ = conn.Close() }()

				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				serve(index, conn)
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

func binaryGreeting() string {
	salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	return fmt.Sprintf("%-63s\n%-63s\n", "Tarantool 3.6.0 (Binary)", salt)
}

func readTestPacket(conn net.Conn) (map[uint64]uint64, error) {
	var length [5]byte

	_, err := io.ReadFull(conn, length[:])
	if err != nil {
		return nil, err
	}

	size := binary.BigEndian.Uint32(length[1:])
	if length[0] != 0xce || size > 4096 {
		return nil, fmt.Errorf("invalid test packet length: %v", length)
	}

	packet := make([]byte, size)

	_, err = io.ReadFull(conn, packet)
	if err != nil {
		return nil, err
	}

	var header map[uint64]uint64

	err = msgpack.NewDecoder(bytes.NewReader(packet)).Decode(&header)

	return header, err
}

func writeTestResponse(conn net.Conn, header map[uint64]uint64) error {
	body := map[uint64]any{0x30: []any{}}

	switch header[0] {
	case 0x49: // IPROTO_ID.
		body = map[uint64]any{0x54: uint64(6), 0x55: []uint64{}}
	case 0x08: // IPROTO_EVAL.
		body = map[uint64]any{0x30: []any{"hello", "world"}}
	}

	return writeTestPacket(conn, map[uint64]uint64{0: 0, 1: header[1]}, body)
}

// writeTestRefusal answers the request of header as Tarantool refuses the
// credentials of an IPROTO_AUTH.
func writeTestRefusal(conn net.Conn, header map[uint64]uint64) error {
	return writeTestPacket(conn, map[uint64]uint64{0: 0x8000 + 47, 1: header[1]},
		map[uint64]any{0x31: "User not found or supplied credentials are invalid"})
}

func writeTestPacket(conn net.Conn, header map[uint64]uint64, body map[uint64]any) error {
	var response bytes.Buffer

	encoder := msgpack.NewEncoder(&response)

	err := encoder.Encode(header)
	if err != nil {
		return err
	}

	err = encoder.Encode(body)
	if err != nil {
		return err
	}

	var length [5]byte

	length[0] = 0xce
	// These fixed test response maps are always smaller than a uint32 length.
	binary.BigEndian.PutUint32(length[1:], uint32(response.Len())) //nolint:gosec

	_, err = conn.Write(append(length[:], response.Bytes()...))

	return err
}

// startTLSTestServer starts a binary server that, as a TLS port of
// Tarantool, greets nobody before the TLS handshake, and returns its address
// and the number of connections it accepted.
func startTLSTestServer(t *testing.T, config *tls.Config) (string, *atomic.Int32) {
	t.Helper()

	return startTLSTestServerAnswering(t, config, writeTestResponse)
}

// startTLSTestServerAnswering starts the server of startTLSTestServer that
// answers each request with respond.
func startTLSTestServerAnswering(t *testing.T, config *tls.Config,
	respond func(net.Conn, map[uint64]uint64) error,
) (string, *atomic.Int32) {
	t.Helper()

	var accepted atomic.Int32

	endpoint := startConnectorTestServer(t, func(_ int, raw net.Conn) {
		accepted.Add(1)

		conn := tls.Server(raw, config)

		err := conn.HandshakeContext(t.Context())
		if err != nil {
			return
		}

		_, err = io.WriteString(conn, binaryGreeting())
		if err != nil {
			return
		}

		for {
			header, err := readTestPacket(conn)
			if err != nil {
				return
			}

			err = respond(conn, header)
			if err != nil {
				return
			}
		}
	})

	return endpoint, &accepted
}

func TestConnectEncryptedSSLKey(t *testing.T) {
	cert, err := tls.LoadX509KeyPair("testdata/localhost.crt", "testdata/localhost.key")
	require.NoError(t, err)

	ca, err := os.ReadFile("testdata/ca.crt")
	require.NoError(t, err)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca))

	cases := []struct {
		name         string
		password     string
		passwordFile string
		wantErr      bool
	}{
		{name: "inline password", password: "secret"},
		{name: "password file tries all lines", passwordFile: "testdata/passwords"},
		{name: "inline password then file", password: "wrong", passwordFile: "testdata/passwords"},
		{name: "wrong password", password: "wrong", wantErr: true},
		{name: "missing password", wantErr: true},
		{name: "missing password file", passwordFile: "testdata/no-password-file", wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			endpoint, _ := startTLSTestServer(t, &tls.Config{
				Certificates: []tls.Certificate{cert},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    roots,
				MinVersion:   tls.VersionTLS12,
			})

			start := time.Now()
			conn, err := Connect(ConnectOpts{
				Network: TCPNetwork, Address: endpoint,
				Username: "test", Password: "password",
				Ssl: SslOpts{
					KeyFile: "testdata/localhost.enc.key", CertFile: "testdata/localhost.crt",
					CaFile: "testdata/ca.crt", Password: testCase.password,
					PasswordFile: testCase.passwordFile,
				},
			})
			elapsed := time.Since(start)

			if testCase.wantErr {
				require.Error(t, err)
				assert.Nil(t, conn)
				// The server is a TLS one: the plain dial after the failure
				// reads no greeting from it and gives up soon.
				require.NotErrorIs(t, err, errEncryptionRequired)
				assert.Less(t, elapsed, greetingOperationTimeout)

				return
			}

			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, conn.Close()) })

			// The setup context is canceled after Connect returns. Requests must
			// still work on the established connection.
			result, err := conn.Eval("return 'hello', 'world'", []any{}, RequestOpts{})
			require.NoError(t, err)
			assert.Equal(t, []any{"hello", "world"}, result)
		})
	}
}

func TestBinaryConnectionSetupTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		auth    bool
	}{
		{name: "default timeout during greeting"},
		{name: "configured timeout during greeting", timeout: 25 * time.Millisecond},
		{
			name:    "configured timeout during authentication",
			timeout: 25 * time.Millisecond,
			auth:    true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			authReached := make(chan struct{}, 1)
			endpoint := startConnectorTestServer(t, func(index int, conn net.Conn) {
				if index == 0 || testCase.auth {
					_, _ = io.WriteString(conn, binaryGreeting())
				}

				if index != 0 && testCase.auth {
					header, err := readTestPacket(conn)
					if err != nil {
						return
					}

					err = writeTestResponse(conn, header)
					if err != nil {
						return
					}

					header, err = readTestPacket(conn)
					if err != nil || header[0] != 0x07 { // IPROTO_AUTH.
						return
					}

					authReached <- struct{}{}
				}

				_, _ = io.Copy(io.Discard, conn)
			})

			parent, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			conn, err := ConnectContext(parent, ConnectOpts{
				Network: TCPNetwork, Address: endpoint, Username: "test", Password: "password",
				ConnectTimeout: testCase.timeout,
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Nil(t, conn)
			require.NoError(t, parent.Err(), "binary setup must use its own timeout")

			if testCase.auth {
				require.NotEmpty(t, authReached, "connection must reach authentication")
			}
		})
	}
}

func TestSSLPasswordOptionsRequireEncryption(t *testing.T) {
	for _, ssl := range []SslOpts{{Password: "secret"}, {PasswordFile: "testdata/passwords"}} {
		endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
			_, _ = io.WriteString(conn, binaryGreeting())
			_, _ = io.Copy(io.Discard, conn)
		})

		conn, err := Connect(ConnectOpts{Network: TCPNetwork, Address: endpoint, Ssl: ssl})
		require.ErrorIs(t, err, errEncryptionRequired)
		assert.Nil(t, conn)
	}
}

func TestSslOptsEncrypted(t *testing.T) {
	cases := []struct {
		name    string
		opts    SslOpts
		want    bool
		wantErr error
	}{
		{name: "no transport, no setting"},
		{name: "no transport, a setting", opts: SslOpts{CaFile: "ca.crt"}, want: true},
		{name: "ssl, no setting", opts: SslOpts{Transport: dial.TransportSsl}, want: true},
		{
			name: "ssl, a setting", want: true,
			opts: SslOpts{Ciphers: "AES256-SHA", Transport: dial.TransportSsl},
		},
		{name: "plain, no setting", opts: SslOpts{Transport: dial.TransportPlain}},
		{
			name:    "plain, a setting",
			opts:    SslOpts{PasswordFile: "passwords", Transport: dial.TransportPlain},
			wantErr: errPlainTransportWithSsl,
		},
		{
			name:    "unknown transport",
			opts:    SslOpts{Transport: dial.Transport(15)},
			wantErr: errUnknownTransport,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encrypted, err := testCase.opts.encrypted()
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Equal(t, testCase.want, encrypted)
		})
	}
}

// The ssl transport with no other option set takes the way the TLS settings
// take: the connection is a binary one over TLS, dialed at once. It used to
// read a greeting first, which a TLS server never sends, and waited out
// greetingOperationTimeout before every TLS connection.
func TestConnectSslTransportWithoutOptions(t *testing.T) {
	cert, err := tls.LoadX509KeyPair("testdata/localhost.crt", "testdata/localhost.key")
	require.NoError(t, err)

	// No CA is given, so the chain of the server goes unchecked, as OpenSSL
	// leaves it, and only its name has to fit.
	hellos := make(chan struct{}, 1)
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	config.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		select {
		case hellos <- struct{}{}:
		default:
		}

		return config, nil
	}

	endpoint, accepted := startTLSTestServer(t, config)

	start := time.Now()
	conn, err := Connect(ConnectOpts{
		Network: TCPNetwork, Address: endpoint, Username: "test", Password: "password",
		Ssl: SslOpts{Transport: dial.TransportSsl},
	})
	elapsed := time.Since(start)

	require.NotEmpty(t, hellos, "the connection must start a TLS handshake")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })
	assert.EqualValues(t, 1, accepted.Load(), "no connection but the TLS one")
	assert.Less(t, elapsed, greetingOperationTimeout, "no wait for a greeting")

	result, err := conn.Eval("return 'hello', 'world'", []any{}, RequestOpts{})
	require.NoError(t, err)
	assert.Equal(t, []any{"hello", "world"}, result)
}

// The ssl transport refuses a server that greets before a TLS handshake, as
// the TLS settings do; the plain transport refuses the TLS settings before
// it dials.
func TestConnectTransportRefusals(t *testing.T) {
	cases := []struct {
		name    string
		ssl     SslOpts
		wantErr error
		dialed  bool
	}{
		{
			name:    "ssl to a plain server",
			ssl:     SslOpts{Transport: dial.TransportSsl},
			wantErr: errEncryptionRequired,
			dialed:  true,
		},
		{
			name:    "plain with a setting",
			ssl:     SslOpts{CaFile: "testdata/ca.crt", Transport: dial.TransportPlain},
			wantErr: errPlainTransportWithSsl,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dials := make(chan struct{}, 2)
			endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
				dials <- struct{}{}

				_, _ = io.WriteString(conn, binaryGreeting())
				_, _ = io.Copy(io.Discard, conn)
			})

			conn, err := Connect(ConnectOpts{
				Network: TCPNetwork, Address: endpoint, Ssl: testCase.ssl,
			})
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Nil(t, conn)
			assert.Equal(t, testCase.dialed, len(dials) != 0)
		})
	}
}

// The plain transport with no other option set connects as no transport
// does.
func TestConnectPlainTransport(t *testing.T) {
	endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
		_, err := io.WriteString(conn, binaryGreeting())
		if err != nil {
			return
		}

		for {
			header, err := readTestPacket(conn)
			if err != nil {
				return
			}

			err = writeTestResponse(conn, header)
			if err != nil {
				return
			}
		}
	})

	conn, err := Connect(ConnectOpts{
		Network: TCPNetwork, Address: endpoint, Username: "test", Password: "password",
		Ssl: SslOpts{Transport: dial.TransportPlain},
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	result, err := conn.Eval("return 'hello', 'world'", []any{}, RequestOpts{})
	require.NoError(t, err)
	assert.Equal(t, []any{"hello", "world"}, result)
}

// A server that is not a TLS one fails the TLS handshake with what it sends
// first. When a plain dial of it then reads a Tarantool greeting, binary or
// of the text console, the server is a plain Tarantool and the failure is
// errEncryptionRequired; with anything else it is that of the handshake.
// Neither waits out greetingOperationTimeout.
func TestConnectTLSToAServerThatIsNotATLSOne(t *testing.T) {
	cases := []struct {
		name    string
		first   string
		wantErr error
	}{
		{name: "a plain Tarantool", first: binaryGreeting(), wantErr: errEncryptionRequired},
		{
			name:    "a text console",
			first:   fmt.Sprintf("%-63s\n%-63s\n", "Tarantool 3.6.0 (Lua console)", "type 'help'"),
			wantErr: errEncryptionRequired,
		},
		{name: "not a Tarantool", first: "HTTP/1.1 400 Bad Request\r\n\r\n"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var accepted atomic.Int32

			endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
				accepted.Add(1)

				_, _ = io.WriteString(conn, testCase.first)
				_, _ = io.Copy(io.Discard, conn)
			})

			start := time.Now()
			conn, err := Connect(ConnectOpts{
				Network: TCPNetwork, Address: endpoint,
				Ssl: SslOpts{Transport: dial.TransportSsl},
			})
			elapsed := time.Since(start)

			assert.Nil(t, conn)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.Error(t, err)
				require.NotErrorIs(t, err, errEncryptionRequired)
			}

			assert.EqualValues(t, 2, accepted.Load(), "the TLS connection and the plain one")
			assert.Less(t, elapsed, greetingOperationTimeout)
		})
	}
}

// A server that stays silent, not even answering the TLS handshake, runs the
// connection into its ConnectTimeout. It is not dialed again: a plain
// Tarantool greets at once and fails the handshake long before that.
func TestConnectTLSToASilentServer(t *testing.T) {
	if !tlsEngineTimesOutHandshake {
		t.Skip("the TLS engine of the build does not time a handshake out")
	}

	var accepted atomic.Int32

	endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
		accepted.Add(1)

		_, _ = io.Copy(io.Discard, conn)
	})

	start := time.Now()
	conn, err := Connect(ConnectOpts{
		Network: TCPNetwork, Address: endpoint,
		Ssl:            SslOpts{Transport: dial.TransportSsl},
		ConnectTimeout: 100 * time.Millisecond,
	})
	elapsed := time.Since(start)

	assert.Nil(t, conn)
	require.Error(t, err)
	require.NotErrorIs(t, err, errEncryptionRequired)
	assert.EqualValues(t, 1, accepted.Load(), "no plain connection after a timeout: %v", err)
	assert.Less(t, elapsed, greetingOperationTimeout)
}

// A TLS server that refuses the credentials answered over TLS, so it is not
// dialed again, and the refusal is the error.
func TestConnectTLSRefusedCredentials(t *testing.T) {
	cert, err := tls.LoadX509KeyPair("testdata/localhost.crt", "testdata/localhost.key")
	require.NoError(t, err)

	endpoint, accepted := startTLSTestServerAnswering(t, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, func(conn net.Conn, header map[uint64]uint64) error {
		if header[0] == 0x07 { // IPROTO_AUTH.
			return writeTestRefusal(conn, header)
		}

		return writeTestResponse(conn, header)
	})

	start := time.Now()
	conn, err := Connect(ConnectOpts{
		Network: TCPNetwork, Address: endpoint, Username: "test", Password: "password",
		Ssl: SslOpts{Transport: dial.TransportSsl},
	})
	elapsed := time.Since(start)

	assert.Nil(t, conn)
	require.ErrorContains(t, err, "User not found or supplied credentials are invalid")
	require.NotErrorIs(t, err, errEncryptionRequired)
	assert.EqualValues(t, 1, accepted.Load(), "no plain connection after a refusal")
	assert.Less(t, elapsed, greetingOperationTimeout)
}

// Cancellation interrupts a TLS connection that waits for the handshake, and
// no plain connection follows it.
func TestConnectTLSCancelled(t *testing.T) {
	if !tlsEngineTimesOutHandshake {
		t.Skip("the TLS engine of the build does not end a handshake on cancellation")
	}

	var accepted atomic.Int32

	ready := make(chan struct{}, 1)
	endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
		accepted.Add(1)

		ready <- struct{}{}

		_, _ = io.Copy(io.Discard, conn)
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := ConnectContext(ctx, ConnectOpts{
			Network: TCPNetwork, Address: endpoint,
			Ssl:            SslOpts{Transport: dial.TransportSsl},
			ConnectTimeout: 10 * time.Second,
		})
		done <- err
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the TLS connection was not dialed")
	}

	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		require.NotErrorIs(t, err, errEncryptionRequired)
	case <-time.After(greetingOperationTimeout):
		t.Fatal("the TLS connection ignored cancellation")
	}

	assert.EqualValues(t, 1, accepted.Load(), "no plain connection after cancellation")
}
