//go:build integration

package connector_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tarantool/v2/test_helpers"
	"github.com/tarantool/go-tlsdialer"

	. "github.com/tarantool/tt/cli/connector"
)

const (
	workDir   = "work_dir"
	server    = "127.0.0.1:3013"
	serverTls = "127.0.0.1:3014"
	console   = workDir + "/" + "console.control"
)

var (
	tarantoolEe bool
	opts        = tarantool.Opts{
		Timeout:    500 * time.Millisecond,
		SkipSchema: true,
	}
	dialer = tarantool.NetDialer{
		Address:  server,
		User:     "test",
		Password: "password",
	}
)

var sslOpts = SslOpts{
	KeyFile:  "testdata/localhost.key",
	CertFile: "testdata/localhost.crt",
	CaFile:   "testdata/ca.crt",
}

func textConnectWithValidation(t *testing.T) *TextConnector {
	t.Helper()

	conn, err := net.Dial("unix", console)
	require.NoError(t, err)

	protocol, err := GetProtocol(conn)
	require.NoError(t, err)
	require.Equal(t, TextProtocol, protocol)

	return NewTextConnector(conn)
}

func binaryConnectWithValidation(t *testing.T) *BinaryConnector {
	conn := test_helpers.ConnectWithValidation(t, dialer, opts)
	return NewBinaryConnector(conn)
}

type testConnect struct {
	protocol Protocol
	connect  Connector
}

func createTestConnects(t *testing.T) []testConnect {
	return []testConnect{
		{TextProtocol, textConnectWithValidation(t)},
		{BinaryProtocol, binaryConnectWithValidation(t)},
	}
}

func TestConnect_Eval(t *testing.T) {
	connects := createTestConnects(t)
	for _, c := range connects {
		defer c.connect.Close()
	}

	for _, c := range connects {
		t.Run(c.protocol.String(), func(t *testing.T) {
			eval := "local val = 'testtest'\n return val"
			opts := RequestOpts{}

			ret, err := c.connect.Eval(eval, []interface{}{}, opts)

			assert.NoError(t, err)
			assert.Equal(t, []interface{}{"testtest"}, ret)
		})
	}
}

func TestBinaryConnector_Eval_args(t *testing.T) {
	connects := createTestConnects(t)
	for _, c := range connects {
		defer c.connect.Close()
	}

	for _, c := range connects {
		t.Run(c.protocol.String(), func(t *testing.T) {
			eval := "return ..."
			opts := RequestOpts{}

			ret, err := c.connect.Eval(eval, []interface{}{"test1", "test2"}, opts)

			assert.NoError(t, err)
			assert.Equal(t, []interface{}{"test1", "test2"}, ret)
		})
	}
}

func TestBinaryConnector_Eval_readTimeout(t *testing.T) {
	connects := createTestConnects(t)
	for _, c := range connects {
		defer c.connect.Close()
	}

	for _, c := range connects {
		t.Run(c.protocol.String(), func(t *testing.T) {
			eval := "require('fiber').sleep(1000)"
			opts := RequestOpts{
				ReadTimeout: 10 * time.Millisecond,
			}

			_, err := c.connect.Eval(eval, []interface{}{}, opts)

			assert.ErrorContains(t, err, "i/o timeout")
		})
	}
}

func TestBinaryConnector_Eval_resData(t *testing.T) {
	connects := createTestConnects(t)
	for _, c := range connects {
		defer c.connect.Close()
	}

	for _, c := range connects {
		t.Run(c.protocol.String(), func(t *testing.T) {
			result := struct {
				Val string
			}{}
			eval := "return 'asd'"
			opts := RequestOpts{
				ResData: &result,
			}
			ret, err := c.connect.Eval(eval, []interface{}{}, opts)

			assert.NoError(t, err)
			assert.Nil(t, ret)
			assert.Equal(t, "asd", result.Val)
		})
	}
}

func TestBinaryConnector_Eval_pushCallback(t *testing.T) {
	connects := createTestConnects(t)
	for _, c := range connects {
		defer c.connect.Close()
	}

	for _, c := range connects {
		t.Run(c.protocol.String(), func(t *testing.T) {
			var pushes []interface{}

			eval := "box.session.push('hello')\n" +
				"box.session.push('world')\n" +
				"return 'return'"
			opts := RequestOpts{
				PushCallback: func(push interface{}) {
					pushes = append(pushes, push)
				},
			}
			ret, err := c.connect.Eval(eval, []interface{}{}, opts)

			assert.NoError(t, err)
			assert.Equal(t, []interface{}{"return"}, ret)
			assert.Equal(t, []interface{}{"hello", "world"}, pushes)
		})
	}
}

func TestConnect_binary(t *testing.T) {
	conn, err := Connect(ConnectOpts{
		Network:  "tcp",
		Address:  server,
		Username: "test",
		Password: "password",
	})
	require.NoError(t, err)
	defer conn.Close()

	eval := "return 'hello', 'world'"
	ret, err := conn.Eval(eval, []interface{}{}, RequestOpts{})
	assert.NoError(t, err)
	assert.Equal(t, []interface{}{"hello", "world"}, ret)
}

func TestConnect_binaryTlsToNoTls(t *testing.T) {
	_, err := Connect(ConnectOpts{
		Network:  "tcp",
		Address:  server,
		Username: "test",
		Password: "password",
		Ssl:      sslOpts,
	})
	expected := "unencrypted connection established, but encryption required"
	require.ErrorContains(t, err, expected)
}

func TestConnect_binaryTlsToTls(t *testing.T) {
	if !tarantoolEe {
		t.Skip("Only for Tarantool Enterprise.")
	}
	conn, err := Connect(ConnectOpts{
		Network:  "tcp",
		Address:  serverTls,
		Username: "test",
		Password: "password",
		Ssl:      sslOpts,
	})
	require.NoError(t, err)
	defer conn.Close()

	eval := "return 'hello', 'world'"
	ret, err := conn.Eval(eval, []interface{}{}, RequestOpts{})
	assert.NoError(t, err)
	assert.Equal(t, []interface{}{"hello", "world"}, ret)
}

func TestConnect_text(t *testing.T) {
	conn, err := Connect(ConnectOpts{
		Network: "unix",
		Address: console,
	})
	require.NoError(t, err)
	defer conn.Close()

	eval := "return 'hello', 'world'"
	ret, err := conn.Eval(eval, []interface{}{}, RequestOpts{})
	assert.NoError(t, err)
	assert.Equal(t, []interface{}{"hello", "world"}, ret)
}

func TestConnect_textTls(t *testing.T) {
	_, err := Connect(ConnectOpts{
		Network: "unix",
		Address: console,
		Ssl:     sslOpts,
	})
	expected := "unencrypted connection established, but encryption required"
	require.ErrorContains(t, err, expected)
}

var poolCases = []struct {
	Name string
	Opts []ConnectOpts
}{
	{
		Name: "single",
		Opts: []ConnectOpts{
			{
				Network:  "tcp",
				Address:  "unreachable",
				Username: "test",
				Password: "password",
			},
			{
				Network:  "tcp",
				Address:  server,
				Username: "test",
				Password: "password",
			},
		},
	},
	{
		Name: "with_invalid",
		Opts: []ConnectOpts{
			{
				Network:  "tcp",
				Address:  server,
				Username: "test",
				Password: "password",
			},
		},
	},
}

func TestPoolConnect_success(t *testing.T) {
	for _, tc := range poolCases {
		t.Run(tc.Name, func(t *testing.T) {
			pool, err := ConnectPool(tc.Opts)
			require.NoError(t, err)
			require.NotNil(t, pool)
			pool.Close()
		})
	}
}

func TestPoolEval_success(t *testing.T) {
	for _, tc := range poolCases {
		t.Run(tc.Name, func(t *testing.T) {
			pool, err := ConnectPool(tc.Opts)
			require.NoError(t, err)
			require.NotNil(t, pool)
			defer pool.Close()

			ret, err := pool.Eval("return ...", []any{"foo"}, RequestOpts{})
			assert.NoError(t, err)
			assert.Equal(t, ret, []any{"foo"})
		})
	}
}

func TestPoolEval_error(t *testing.T) {
	for _, tc := range poolCases {
		t.Run(tc.Name, func(t *testing.T) {
			pool, err := ConnectPool(tc.Opts)
			require.NoError(t, err)
			require.NotNil(t, pool)
			defer pool.Close()

			for i := 0; i < 10; i++ {
				_, err = pool.Eval("error('foo')", []any{"foo"}, RequestOpts{})
				assert.ErrorContains(t, err, "foo")
			}
		})
	}
}

func runTestMain(m *testing.M) int {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		fmt.Println("Failed to prepare test work dir:", err)
		return 1
	}

	inst, err := test_helpers.StartTarantool(test_helpers.StartOpts{
		InitScript:   "testdata/config.lua",
		Listen:       server,
		WorkDir:      absWorkDir,
		WaitStart:    5 * time.Second,
		ConnectRetry: 5,
		RetryTimeout: 100 * time.Millisecond,
		Dialer:       dialer,
	})
	if err != nil {
		fmt.Println("Failed to prepare test tarantool:", err)
		return 1
	}
	defer test_helpers.StopTarantoolWithCleanup(inst)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	conn, err := tarantool.Connect(ctx, dialer, opts)
	if err != nil {
		fmt.Println("Failed to check tarantool version:", err)
		return 1
	}
	req := tarantool.NewEvalRequest("return box.info.package")
	data, err := conn.Do(req).Get()
	conn.Close()

	if err != nil {
		fmt.Println("Failed to get box.info.package:", err)
		return 1
	}

	if len(data) > 0 {
		if pack, ok := data[0].(string); ok {
			tarantoolEe = pack == "Tarantool Enterprise"
		}
	}

	if tarantoolEe {
		absTLSWorkDir, err := filepath.Abs(workDir + "_tls")
		if err != nil {
			fmt.Println("Failed to prepare TLS test work dir:", err)
			return 1
		}

		// Try to start Tarantool instance with TLS.
		listen := serverTls + "?transport=ssl&" +
			"ssl_key_file=testdata/localhost.key&" +
			"ssl_cert_file=testdata/localhost.crt&" +
			"ssl_ca_file=testdata/ca.crt"

		tlsDialer := tlsdialer.OpenSSLDialer{
			Address:     serverTls,
			User:        dialer.User,
			Password:    dialer.Password,
			SslKeyFile:  sslOpts.KeyFile,
			SslCertFile: sslOpts.CertFile,
			SslCaFile:   sslOpts.CaFile,
		}
		inst, err = test_helpers.StartTarantool(test_helpers.StartOpts{
			InitScript:   "testdata/config.lua",
			Listen:       listen,
			SslCertsDir:  "testdata",
			WorkDir:      absTLSWorkDir,
			WaitStart:    time.Second,
			ConnectRetry: 5,
			RetryTimeout: 200 * time.Millisecond,
			Dialer:       tlsDialer,
		})
		if err != nil {
			fmt.Println("Failed to prepare test tarantool with TLS:", err)
			return 1
		}
		defer test_helpers.StopTarantoolWithCleanup(inst)
	}

	return m.Run()
}

func TestMain(m *testing.M) {
	code := runTestMain(m)
	os.Exit(code)
}
