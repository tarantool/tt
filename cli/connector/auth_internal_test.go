package connector

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/vmihailenco/msgpack/v5"
)

// The keys and the request types of IPROTO the test server answers.
const (
	keyRequestType = 0x00
	keySync        = 0x01
	keyTuple       = 0x21
	keyData        = 0x30
	keyVersion     = 0x54
	keyFeatures    = 0x55
	keyAuthType    = 0x5b

	typeOK   = 0x00
	typeAuth = 0x07
	typeID   = 0x49
)

// authServer answers the requests of a binary connection as Tarantool of
// auth_type authType does, and keeps the methods of the IPROTO_AUTH requests
// it is sent.
type authServer struct {
	authType string

	mu      sync.Mutex
	methods []string
}

func (s *authServer) authMethods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.methods...)
}

func (s *authServer) serve(conn net.Conn) {
	_, err := io.WriteString(conn, binaryGreeting())
	if err != nil {
		return
	}

	for {
		header, body, err := readAuthTestPacket(conn)
		if err != nil {
			return
		}

		reply := map[uint64]any{keyData: []any{"hello", "world"}}

		switch header[keyRequestType] {
		case typeID:
			reply = map[uint64]any{
				keyVersion:  uint64(6),
				keyFeatures: []uint64{},
				keyAuthType: s.authType,
			}
		case typeAuth:
			tuple, _ := body[keyTuple].([]any)
			if len(tuple) > 0 {
				method, _ := tuple[0].(string)

				s.mu.Lock()

				s.methods = append(s.methods, method)

				s.mu.Unlock()
			}

			reply = map[uint64]any{}
		}

		err = writeAuthTestPacket(conn, header[keySync], reply)
		if err != nil {
			return
		}
	}
}

func readAuthTestPacket(conn net.Conn) (map[uint64]uint64, map[uint64]any, error) {
	var length [5]byte

	_, err := io.ReadFull(conn, length[:])
	if err != nil {
		return nil, nil, err
	}

	size := binary.BigEndian.Uint32(length[1:])
	if length[0] != 0xce || size > 4096 {
		return nil, nil, fmt.Errorf("invalid test packet length: %v", length)
	}

	packet := make([]byte, size)

	_, err = io.ReadFull(conn, packet)
	if err != nil {
		return nil, nil, err
	}

	decoder := msgpack.NewDecoder(bytes.NewReader(packet))

	var (
		header map[uint64]uint64
		body   map[uint64]any
	)

	err = decoder.Decode(&header)
	if err != nil {
		return nil, nil, err
	}

	err = decoder.Decode(&body)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}

	return header, body, nil
}

func writeAuthTestPacket(conn net.Conn, sync uint64, body map[uint64]any) error {
	var packet bytes.Buffer

	encoder := msgpack.NewEncoder(&packet)

	err := encoder.Encode(map[uint64]uint64{keyRequestType: typeOK, keySync: sync})
	if err != nil {
		return err
	}

	err = encoder.Encode(body)
	if err != nil {
		return err
	}

	var length [5]byte

	length[0] = 0xce
	// The packets of the test are far smaller than a uint32 length.
	binary.BigEndian.PutUint32(length[1:], uint32(packet.Len())) //nolint:gosec

	_, err = conn.Write(append(length[:], packet.Bytes()...))

	return err
}

// startAuthServer starts server on a plain port, or on a TLS one, which
// greets nobody before the handshake, when tlsConfig is set. probed says
// that the first connection is the probe of the protocol Connect makes.
func startAuthServer(t *testing.T, server *authServer, tlsConfig *tls.Config,
	probed bool,
) string {
	t.Helper()

	return startConnectorTestServer(t, func(index int, raw net.Conn) {
		if tlsConfig == nil {
			server.serve(raw)

			return
		}

		if probed && index == 0 {
			// End the plaintext probe so the connector can start TLS.
			return
		}

		conn := tls.Server(raw, tlsConfig)

		err := conn.HandshakeContext(t.Context())
		if err != nil {
			return
		}

		server.serve(conn)
	})
}

func authTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	cert, err := tls.LoadX509KeyPair("testdata/localhost.crt", "testdata/localhost.key")
	require.NoError(t, err)

	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

// connectFunc opens a binary connection the way one of the entry points of
// the package does, and evaluates a request on it.
type connectFunc func(t *testing.T, opts ConnectOpts) error

func connectEval(t *testing.T, opts ConnectOpts) error {
	t.Helper()

	conn, err := Connect(opts)
	if err != nil {
		return err
	}

	defer func() { assert.NoError(t, conn.Close()) }()

	result, err := conn.Eval("return 'hello', 'world'", []any{}, RequestOpts{})
	require.NoError(t, err)
	assert.Equal(t, []any{"hello", "world"}, result)

	return nil
}

func connectIprotoEval(t *testing.T, opts ConnectOpts) error {
	t.Helper()

	conn, err := ConnectIproto(t.Context(), opts, tarantool.Opts{})
	if err != nil {
		return err
	}

	defer func() { assert.NoError(t, conn.Close()) }()

	_, err = conn.Do(tarantool.NewEvalRequest("return 'hello', 'world'")).Get()
	require.NoError(t, err)

	return nil
}

var connectFuncs = map[string]connectFunc{
	"Connect":       connectEval,
	"ConnectIproto": connectIprotoEval,
}

// The authentication method of the options decides the one a binary
// connection authenticates with: over TLS the automatic one is the
// server's, over a plain connection it is chap-sha1.
func TestConnectAuth(t *testing.T) {
	const (
		chap = "chap-sha1"
		pap  = "pap-sha256"
	)

	cases := []struct {
		ssl        bool
		serverAuth string
		auth       dial.Auth
		want       string
	}{
		{ssl: true, serverAuth: pap, auth: dial.AuthAuto, want: pap},
		{ssl: true, serverAuth: chap, auth: dial.AuthAuto, want: chap},
		{ssl: true, serverAuth: chap, auth: dial.AuthPapSha256, want: pap},
		{ssl: true, serverAuth: pap, auth: dial.AuthChapSha1, want: chap},
		{ssl: false, serverAuth: pap, auth: dial.AuthAuto, want: chap},
		{ssl: false, serverAuth: chap, auth: dial.AuthAuto, want: chap},
		{ssl: false, serverAuth: pap, auth: dial.AuthChapSha1, want: chap},
	}

	for _, testCase := range cases {
		for name, connect := range connectFuncs {
			title := fmt.Sprintf("%s, ssl %t, server %s, %s", name, testCase.ssl,
				testCase.serverAuth, testCase.auth)

			t.Run(title, func(t *testing.T) {
				server := &authServer{authType: testCase.serverAuth}

				var (
					tlsConfig *tls.Config
					ssl       SslOpts
				)

				if testCase.ssl {
					tlsConfig = authTLSConfig(t)
					ssl = SslOpts{Transport: dial.TransportSsl}
				}

				endpoint := startAuthServer(t, server, tlsConfig, name == "Connect")

				err := connect(t, ConnectOpts{
					Network: TCPNetwork, Address: endpoint,
					Username: "test", Password: "password",
					Auth: testCase.auth, Ssl: ssl,
				})
				require.NoError(t, err)
				assert.Equal(t, []string{testCase.want}, server.authMethods())
			})
		}
	}
}

// pap-sha256 over a plain connection is refused before the address is
// dialed: the password is not sent unencrypted, nor is the port probed.
func TestConnectPapSha256Plain(t *testing.T) {
	for _, transport := range []dial.Transport{dial.TransportDefault, dial.TransportPlain} {
		for name, connect := range map[string]func(ConnectOpts) error{
			"Connect": func(opts ConnectOpts) error {
				_, err := ConnectContext(context.Background(), opts)
				return err
			},
			"ConnectIproto": func(opts ConnectOpts) error {
				_, err := ConnectIproto(context.Background(), opts, tarantool.Opts{})
				return err
			},
		} {
			t.Run(fmt.Sprintf("%s, transport %q", name, transport), func(t *testing.T) {
				dials := make(chan struct{}, 2)
				endpoint := startConnectorTestServer(t, func(_ int, conn net.Conn) {
					dials <- struct{}{}

					_, _ = io.WriteString(conn, binaryGreeting())
					_, _ = io.Copy(io.Discard, conn)
				})

				err := connect(ConnectOpts{
					Network: TCPNetwork, Address: endpoint,
					Username: "test", Password: "password",
					Auth: dial.AuthPapSha256, Ssl: SslOpts{Transport: transport},
				})
				require.ErrorIs(t, err, dial.ErrPapSha256Unencrypted)
				assert.Empty(t, dials, "nothing is dialed")
			})
		}
	}
}
