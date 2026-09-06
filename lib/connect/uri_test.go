package connect_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/lib/connect"
)

// spell-checker:ignore kfile

const (
	testUser     = "a-фs$d!e%*1#2?3&44"
	testPass     = "bb-фs$d!e%*1#2?3&666"
	testUserPass = testUser + ":" + testPass
)

var validBaseUris = []string{
	"tcp://localhost:11",
	"localhost:123",
	"host:123",
	"123:123",
	"tcp://127.0.0.1:123",
	"127.0.0.1:123",
	"tcp://[::1]:123",
	"[::1]:123",
	"unix://path",
	"unix://path/to/file",
	"unix:///path/to/file",
	"unix://../path/to/file",
	"./a",
	"/1",
	"../a",
	".//a",
	"~/a",
	"..//..//file",
}

var validCredentialsUris = []string{
	"tcp://" + testUserPass + "@localhost:11",
	testUserPass + "@localhost:123",
	"tcp://" + testUserPass + "@127.0.0.1:123",
	testUserPass + "@127.0.0.1:123",
	"tcp://" + testUserPass + "@[::1]:1234",
	testUserPass + "@[::1]:1234",
	"unix://" + testUserPass + "@path",
	"unix://" + testUserPass + "@../path/to/file",
	"unix://" + testUserPass + "@//path",
	testUserPass + "@./a",
	testUserPass + "@/1",
	testUserPass + "@.//a",
	testUserPass + "@../a",
	testUserPass + "@~/a",
	testUserPass + "@//path",
	"https://" + testUserPass + "@localhost:2379/prefix",
	"https://" + testUserPass + "@localhost:2379",
}

var invalidBaseUris = []string{
	"tcp:localhost:123123",
	"tcp:/anyhost:1", // spell-checker:ignore anyhost
	"tcp://localhost:asd",
	"tcp:///localhost:11",
	"asd://localhost:111",
	"123://localhost:123",
	"123asd:localhost:222",
	"123",
	"localhost",
	"localhost:asd",
	"unix:",
	"unix:a",
	"unix:/",
	"unix:/a",
	"unix/:",
	"unix/:2",
	"unix//:asd",
	"unix/:/",
	"unix://",
	"unix://.",
	"unix:///",
	".",
	".a",
	"/",
	"~.",
	"~~~~~~/a",
	".../a",
}

var invalidCredentialsUris = []string{
	"tcp://user@localhost:11",
	"user:password@tcp://localhost:11",
	"user@localhost:123",
	"unix://user@path",
	"user:password@unix://path",
	"unix://user@../path/to/file",
	"user:password@unix://../path/to/file",
	"user@./a",
	"user@/1",
	"user:password@~./",
	"user:password@~~/",
	"user:password@../",
}

func TestIsBaseURIValid(t *testing.T) {
	for _, uri := range validBaseUris {
		t.Run(uri, func(t *testing.T) {
			assert.True(t, connect.IsBaseURI(uri), "URI must be valid")
		})
	}
}

func TestIsBaseURIInvalid(t *testing.T) {
	invalid := make([]string, 0,
		len(invalidBaseUris)+len(validCredentialsUris)+len(invalidCredentialsUris))
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, validCredentialsUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			assert.False(t, connect.IsBaseURI(uri), "URI must be invalid")
		})
	}
}

func TestIsCredentialsURIValid(t *testing.T) {
	for _, uri := range validCredentialsUris {
		t.Run(uri, func(t *testing.T) {
			assert.True(t, connect.IsCredentialsURI(uri), "URI must be valid")
		})
	}
}

func TestIsCredentialsURIInvalid(t *testing.T) {
	invalid := make([]string, 0,
		len(validBaseUris)+len(invalidBaseUris)+len(invalidCredentialsUris))
	invalid = append(invalid, validBaseUris...)
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			assert.False(t, connect.IsCredentialsURI(uri), "URI must be invalid")
		})
	}
}

func TestParseCredentialsURI(t *testing.T) {
	cases := []struct {
		srcURI string
		newURI string
	}{
		{"tcp://" + testUserPass + "@localhost:3013", "tcp://localhost:3013"},
		{testUserPass + "@localhost:3013", "localhost:3013"},
		{"tcp://" + testUserPass + "@127.0.0.1:3013", "tcp://127.0.0.1:3013"},
		{testUserPass + "@127.0.0.1:3013", "127.0.0.1:3013"},
		{"tcp://" + testUserPass + "@[::1]:3013", "tcp://[::1]:3013"},
		{testUserPass + "@[::1]:3013", "[::1]:3013"},
		{"unix://" + testUserPass + "@/any/path", "unix:///any/path"},
		{testUserPass + "@/path", "/path"},
		{testUserPass + "@./path", "./path"},
		{testUserPass + "@../path", "../path"},
		{testUserPass + "@.//a", ".//a"},
		{testUserPass + "@~/a", "~/a"},
		{"unix://" + testUserPass + "@~/a/b", "unix://~/a/b"},
		{"unix://" + testUserPass + "@~/../a", "unix://~/../a"},
	}

	for _, c := range cases {
		t.Run(c.srcURI, func(t *testing.T) {
			newURI, user, pass := connect.ParseCredentialsURI(c.srcURI)
			assert.Equal(t, c.newURI, newURI, "a unexpected new URI")
			assert.Equal(t, testUser, user, "a unexpected username")
			assert.Equal(t, testPass, pass, "a unexpected password")
		})
	}
}

func TestParseCredentialsURI_parseValid(t *testing.T) {
	for _, uri := range validCredentialsUris {
		t.Run(uri, func(t *testing.T) {
			newURI, user, pass := connect.ParseCredentialsURI(uri)
			assert.NotEqual(t, uri, newURI, "URI must change")
			assert.NotEqual(t, "", user, "username must not be empty")
			assert.NotEqual(t, "", pass, "password must not be empty")
		})
	}
}

func TestParseCredentialsURI_notParseInvalid(t *testing.T) {
	invalid := make([]string, 0,
		len(validBaseUris)+len(invalidBaseUris)+len(invalidCredentialsUris))
	invalid = append(invalid, validBaseUris...)
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			newURI, user, pass := connect.ParseCredentialsURI(uri)
			assert.Equal(t, uri, newURI, "URI must no change")
			assert.Equal(t, "", user, "username must be empty")
			assert.Equal(t, "", pass, "password must be empty")
		})
	}
}

func TestParseBaseURI(t *testing.T) {
	cases := []struct {
		URI     string
		network string
		address string
	}{
		{"localhost:3013", connect.TCPNetwork, "localhost:3013"},
		{"tcp://localhost:3013", connect.TCPNetwork, "localhost:3013"},
		{"127.0.0.1:3013", connect.TCPNetwork, "127.0.0.1:3013"},
		{"tcp://127.0.0.1:3013", connect.TCPNetwork, "127.0.0.1:3013"},
		{"[::1]:3013", connect.TCPNetwork, "[::1]:3013"},
		{"tcp://[::1]:3013", connect.TCPNetwork, "[::1]:3013"},
		{"./path/to/socket", connect.UnixNetwork, "./path/to/socket"},
		{"/path/to/socket", connect.UnixNetwork, "/path/to/socket"},
		{"unix:///path/to/socket", connect.UnixNetwork, "/path/to/socket"},
		{"unix://..//path/to/socket", connect.UnixNetwork, "..//path/to/socket"},
		{"..//path", connect.UnixNetwork, "..//path"},
		{"some_uri", connect.TCPNetwork, "some_uri"}, // Keeps unchanged.
	}

	for _, tc := range cases {
		t.Run(tc.URI, func(t *testing.T) {
			network, address := connect.ParseBaseURI(tc.URI)
			assert.Equal(t, network, tc.network)
			assert.Equal(t, address, tc.address)
		})
	}

	t.Run("starts from ~", func(t *testing.T) {
		homeDir, _ := os.UserHomeDir()
		network, address := connect.ParseBaseURI("unix://~/a/b")
		assert.Equal(t, connect.UnixNetwork, network)
		assert.Equal(t, homeDir+"/a/b", address)

		network, address = connect.ParseBaseURI("~/a/b")
		assert.Equal(t, connect.UnixNetwork, network)
		assert.Equal(t, homeDir+"/a/b", address)
	})
}

// spell-checker:ignore anyname cafile capath
func TestParseUriOpts(t *testing.T) {
	const defaultTimeout = 3 * time.Second

	cases := map[string]struct {
		URL    string
		Opts   connect.URIOpts
		params map[string]string
		Err    string
	}{
		"empty url": {
			URL:  "",
			Opts: connect.URIOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"no scheme": {
			URL:  "host",
			Opts: connect.URIOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"invalid scheme": {
			URL:  ":host",
			Opts: connect.URIOpts{},
			Err:  "missing protocol scheme",
		},
		"no host": {
			URL:  "scheme:///prefix",
			Opts: connect.URIOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"with opaque": {
			URL:  "scheme:host.com/prefix",
			Opts: connect.URIOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"simple": {
			URL: "scheme://localhost",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with port": {
			URL: "scheme://localhost:3013",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost:3013",
				Host:     "localhost:3013",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"user auth": {
			URL: "scheme://user@localhost",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"user and pass": {
			URL: "scheme://user:pass@localhost",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Password: "pass",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"prefix root": {
			URL: "scheme://localhost/",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with prefix": {
			URL: "scheme://localhost/prefix",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with prefix and fragment": {
			URL: "scheme://localhost/prefix#Fragment",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Tag:      "Fragment",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"only fragment": {
			URL: "scheme://localhost#Fragment",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Tag:      "Fragment",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with param key": {
			URL: "scheme://localhost/prefix?key=anykey",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"key": "anykey"},
			},
			Err: "",
		},
		"with param name": {
			URL: "scheme://localhost/prefix?name=anyname",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"name": "anyname"},
			},
			Err: "",
		},
		"no prefix with params": {
			URL: "scheme://localhost?name=anyname#Fragment",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Tag:      "Fragment",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"name": "anyname"},
			},
			Err: "",
		},
		"with empty param": {
			URL: "scheme://localhost/prefix?name=",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"name": ""},
			},
			Err: "",
		},
		"ssl_key_file": {
			URL: "scheme://localhost?ssl_key_file=/any/kfile",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				KeyFile:  "/any/kfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"ssl_cert_file": {
			URL: "scheme://localhost?ssl_cert_file=/any/certfile",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CertFile: "/any/certfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"ssl_ca_path": {
			URL: "scheme://localhost?ssl_ca_path=/any/capath",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaPath:   "/any/capath",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"ssl_ca_file": {
			URL: "scheme://localhost?ssl_ca_file=/any/cafile",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaFile:   "/any/cafile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"verify peer and host": {
			URL: "scheme://localhost?verify_peer=true&verify_host=true",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"verify peer and host is empty": {
			URL: "scheme://localhost?verify_peer=&verify_host=",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"skip verify peer": {
			URL: "scheme://localhost?verify_peer=false",
			Opts: connect.URIOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipPeerVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		"invalid verify_peer": {
			URL:  "scheme://localhost?verify_peer=asd",
			Opts: connect.URIOpts{},
			Err:  `invalid "verify_peer" param, boolean expected:`,
		},
		"skip verify host": {
			URL: "scheme://localhost?verify_host=false",
			Opts: connect.URIOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipHostVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		"invalid verify_host": {
			URL:  "scheme://localhost?verify_host=asd",
			Opts: connect.URIOpts{},
			Err:  `invalid "verify_host" param, boolean expected:`,
		},
		"timeout": {
			URL: "scheme://localhost?timeout=5.5",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  time.Duration(float64(5.5) * float64(time.Second)),
			},
			Err: "",
		},
		"empty timeout": {
			URL: "scheme://localhost?timeout=",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"invalid timeout": {
			URL:  "scheme://localhost?timeout=asd",
			Opts: connect.URIOpts{},
			Err:  `invalid "timeout" param, float (in seconds) expected:`,
		},
		"full set": {
			URL: "scheme://user:pass@localhost:2012/prefix" +
				"?key=anykey&name=anyname" +
				"&ssl_key_file=kfile&ssl_cert_file=certfile" +
				"&ssl_ca_path=capath&ssl_ca_file=cafile" +
				"&ssl_ciphers=foo:bar:ciphers" +
				"&verify_peer=true&verify_host=false&timeout=2" +
				"#Fragment",
			Opts: connect.URIOpts{
				Endpoint:       "scheme://localhost:2012",
				Host:           "localhost:2012",
				Prefix:         "/prefix",
				Tag:            "Fragment",
				Username:       "user",
				Password:       "pass",
				KeyFile:        "kfile",
				CertFile:       "certfile",
				CaPath:         "capath",
				CaFile:         "cafile",
				Ciphers:        "foo:bar:ciphers",
				SkipHostVerify: true,
				Timeout:        2 * time.Second,
				Params:         map[string]string{"key": "anykey", "name": "anyname"},
			},
			Err: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.Opts.Params == nil {
				tc.Opts.Params = make(map[string]string)
			}
			opts, err := connect.CreateURIOpts(tc.URL)
			if tc.Err != "" {
				assert.ErrorContains(t, err, tc.Err)
			} else {
				assert.Equal(t, tc.Opts, opts)
			}
		})
	}
}
