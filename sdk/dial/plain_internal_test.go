package dial

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/vmihailenco/msgpack/v5"
)

// credsMismatchMsg is the message of ER_CREDS_MISMATCH.
const credsMismatchMsg = "User not found or supplied credentials are invalid" //nolint:gosec

// fakeServer is a binary port of Tarantool enough for a dialer: it greets,
// answers IPROTO_ID with its auth_type and IPROTO_AUTH as it is told, and
// keeps the authentication methods the requests name.
type fakeServer struct {
	// authType is the auth_type IPROTO_ID names; empty names none, as a
	// Tarantool before 2.11 does.
	authType string
	// refuse makes IPROTO_AUTH fail with ER_CREDS_MISMATCH.
	refuse bool

	mu      sync.Mutex
	methods []string
}

func (s *fakeServer) authMethods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.methods...)
}

func (s *fakeServer) start(t *testing.T) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

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

				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				s.serve(conn)
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

func (s *fakeServer) serve(conn net.Conn) {
	salt := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))

	_, err := fmt.Fprintf(conn, "%-63s\n%-63s\n", "Tarantool 3.9.0 (Binary)", salt)
	if err != nil {
		return
	}

	for {
		header, body, err := readPacket(conn)
		if err != nil {
			return
		}

		code := uint64(iproto.IPROTO_OK)
		reply := map[iproto.Key]any{iproto.IPROTO_DATA: []any{}}

		switch header[iproto.IPROTO_REQUEST_TYPE] {
		case uint64(iproto.IPROTO_ID):
			reply = map[iproto.Key]any{
				iproto.IPROTO_VERSION:  uint64(6),
				iproto.IPROTO_FEATURES: []uint64{},
			}
			if s.authType != "" {
				reply[iproto.IPROTO_AUTH_TYPE] = s.authType
			}
		case uint64(iproto.IPROTO_AUTH):
			if tuple, ok := body[iproto.IPROTO_TUPLE].([]any); ok && len(tuple) > 0 {
				method, _ := tuple[0].(string)

				s.mu.Lock()

				s.methods = append(s.methods, method)

				s.mu.Unlock()
			}

			if s.refuse {
				code = uint64(iproto.IPROTO_TYPE_ERROR) | uint64(iproto.ER_CREDS_MISMATCH)
				reply = map[iproto.Key]any{iproto.IPROTO_ERROR_24: credsMismatchMsg}
			}
		}

		err = writePacket(conn, map[iproto.Key]uint64{
			iproto.IPROTO_REQUEST_TYPE: code,
			iproto.IPROTO_SYNC:         header[iproto.IPROTO_SYNC],
		}, reply)
		if err != nil {
			return
		}
	}
}

func readPacket(conn net.Conn) (map[iproto.Key]uint64, map[iproto.Key]any, error) {
	var length [5]byte

	_, err := io.ReadFull(conn, length[:])
	if err != nil {
		return nil, nil, err
	}

	size := binary.BigEndian.Uint32(length[1:])
	if length[0] != 0xce || size > 4096 {
		return nil, nil, fmt.Errorf("invalid packet length: %v", length)
	}

	packet := make([]byte, size)

	_, err = io.ReadFull(conn, packet)
	if err != nil {
		return nil, nil, err
	}

	decoder := msgpack.NewDecoder(bytes.NewReader(packet))

	var (
		header map[iproto.Key]uint64
		body   map[iproto.Key]any
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

func writePacket(conn net.Conn, header map[iproto.Key]uint64, body map[iproto.Key]any) error {
	var packet bytes.Buffer

	encoder := msgpack.NewEncoder(&packet)

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
	// The packets of the test are far smaller than a uint32 length.
	binary.BigEndian.PutUint32(length[1:], uint32(packet.Len())) //nolint:gosec

	_, err = conn.Write(append(length[:], packet.Bytes()...))

	return err
}

func dialPlain(t *testing.T, opts Opts) (tarantool.Conn, error) {
	t.Helper()

	dialer, err := New(opts)
	require.NoError(t, err)

	conn, err := dialer.Dial(t.Context(), tarantool.DialOpts{IoTimeout: 5 * time.Second})
	if conn != nil {
		t.Cleanup(func() { _ = conn.Close() })
	}

	return conn, err
}

// The plain transport authenticates with chap-sha1 whatever the auth_type of
// the server is, the automatic method and chap-sha1 alike; a server of
// pap-sha256 takes it from a user whose password predates the auth_type.
func TestPlainDialer_chapSha1(t *testing.T) {
	cases := []struct {
		authType string
		auth     tarantool.Auth
	}{
		{"", tarantool.AutoAuth},
		{"chap-sha1", tarantool.AutoAuth},
		{"pap-sha256", tarantool.AutoAuth},
		{"chap-sha1", tarantool.ChapSha1Auth},
		{"pap-sha256", tarantool.ChapSha1Auth},
	}

	for _, testCase := range cases {
		t.Run(fmt.Sprintf("server %q, %s", testCase.authType, testCase.auth), func(t *testing.T) {
			server := &fakeServer{authType: testCase.authType}
			address := server.start(t)

			conn, err := dialPlain(t, Opts{
				Address: address, Auth: testCase.auth, User: "user", Password: "secret",
			})
			require.NoError(t, err)
			require.NotNil(t, conn)
			assert.Equal(t, []string{"chap-sha1"}, server.authMethods())
		})
	}
}

// A guest connection authenticates nobody.
func TestPlainDialer_guest(t *testing.T) {
	server := &fakeServer{authType: "pap-sha256", refuse: true}
	address := server.start(t)

	conn, err := dialPlain(t, Opts{Address: address})
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Empty(t, server.authMethods())
}

// A refusal of the credentials by a server of pap-sha256 says that the
// method takes the ssl transport; one by any other server is as it was.
func TestPlainDialer_refused(t *testing.T) {
	cases := []struct {
		authType string
		hint     bool
	}{
		{"", false},
		{"chap-sha1", false},
		{"pap-sha256", true},
	}

	for _, testCase := range cases {
		t.Run(fmt.Sprintf("server %q", testCase.authType), func(t *testing.T) {
			server := &fakeServer{authType: testCase.authType, refuse: true}
			address := server.start(t)

			conn, err := dialPlain(t, Opts{Address: address, User: "user", Password: "wrong"})
			require.ErrorContains(t, err, credsMismatchMsg)
			assert.Nil(t, conn)
			assert.True(t, isCredsMismatch(err), "the server error must stay in the chain")
			assert.Equal(t, testCase.hint, errors.Is(err, ErrServerAuthPapSha256))
			assert.Equal(t, []string{"chap-sha1"}, server.authMethods())
		})
	}
}
