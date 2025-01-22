package connect

import (
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool"
)

func TestMakeConnectOptsFromUriOpts(t *testing.T) {
	cases := []struct {
		Name         string
		UriOpts      UriOpts
		Expected     tarantool.Opts
		ExpectedAddr string
	}{
		{
			Name:         "empty",
			UriOpts:      UriOpts{},
			Expected:     tarantool.Opts{},
			ExpectedAddr: "tcp://",
		},
		{
			Name: "ignored",
			UriOpts: UriOpts{
				Endpoint:       "localhost:3013",
				Prefix:         "foo",
				Key:            "bar",
				Instance:       "zoo",
				CaPath:         "ca_path",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        673,
			},
			Expected: tarantool.Opts{
				Timeout: 673,
			},
			ExpectedAddr: "tcp://", // is this ok?
		},
		{
			Name: "full",
			UriOpts: UriOpts{
				Endpoint:       "scheme://foo",
				Host:           "foo",
				Prefix:         "prefix",
				Key:            "key",
				Instance:       "instance",
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
			Expected: tarantool.Opts{
				User:      "username",
				Pass:      "password",
				Timeout:   2 * time.Second,
				Transport: "ssl",
				Ssl: tarantool.SslOpts{
					KeyFile:  "key_file",
					CertFile: "cert_file",
					CaFile:   "ca_file",
					Ciphers:  "foo:bar:ciphers",
				},
			},
			ExpectedAddr: "tcp://foo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			addr, tntOpts := makeConnectOptsFromUriOpts(tc.UriOpts)

			assert.Equal(t, tc.Expected, tntOpts)
			assert.Equal(t, tc.ExpectedAddr, addr)
		})
	}
}

type mockConnector struct {
	t       *testing.T
	addr    string
	opts    tarantool.Opts
	needErr bool
}

func (m *mockConnector) Connect(addr string, opts tarantool.Opts) (conn *tarantool.Connection, err error) {
	assert.Equal(m.t, m.addr, addr)
	assert.Equal(m.t, m.opts, opts)
	if m.needErr {
		return nil, errors.New("connect error")
	}
	return nil, nil
}

func TestRunOnTarantool(t *testing.T) {
	type args struct {
		url  string
		f    TarantoolFunc
		user string
		pwd  string
		env  map[string]string
	}
	tests := []struct {
		name    string
		args    args
		addr    string
		opts    tarantool.Opts
		want    bool
		wantErr bool
	}{
		{
			"Nil function",
			args{
				url: "http://localhost:1234",
				f:   nil,
			},
			"tcp://localhost:1234",
			tarantool.Opts{
				Timeout: DefaultUriTimeout,
			},
			false,
			false,
		},
		{
			"Function called",
			args{
				url: "http://user:pass@localhost:1234",
				f: func(c tarantool.Connector) error {
					return nil
				},
			},
			"tcp://localhost:1234",
			tarantool.Opts{
				Timeout: DefaultUriTimeout,
				User:    "user",
				Pass:    "pass",
			},
			true,
			false,
		},
		{
			"Environment passed",
			args{
				url: "http://localhost:1234",
				f: func(c tarantool.Connector) error {
					return nil
				},
				env: map[string]string{
					TarantoolUsernameEnv: "env_user",
					TarantoolPasswordEnv: "env_pass",
				},
			},
			"tcp://localhost:1234",
			tarantool.Opts{
				Timeout: DefaultUriTimeout,
				User:    "env_user",
				Pass:    "env_pass",
			},
			true,
			false,
		},
		{
			"Error connections",
			args{
				url: "http://localhost:1234",
				f: func(c tarantool.Connector) error {
					return nil
				},
			},
			"tcp://localhost:1234",
			tarantool.Opts{
				Timeout: DefaultUriTimeout,
			},
			false,
			true,
		},
	}
	mc := mockConnector{t: t}
	tarantoolConnect = mc.Connect
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := url.Parse(tt.args.url)
			require.NoError(t, err)

			opts, err := ParseUriOpts(uri, tt.args.user, tt.args.pwd)
			require.NoError(t, err)

			for k, v := range tt.args.env {
				require.NoError(t, os.Setenv(k, v))
				defer os.Unsetenv(k)
			}
			mc.addr = tt.addr
			mc.opts = tt.opts
			mc.needErr = tt.wantErr
			got, err := RunOnTarantool(opts, tt.args.f)
			if (err != nil) != tt.wantErr {
				t.Errorf("RunOnTarantool() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("RunOnTarantool() = %v, want %v", got, tt.want)
			}
		})
	}
}
