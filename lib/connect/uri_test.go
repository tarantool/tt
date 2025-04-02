package connect_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/lib/connect"
)

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
	"tcp:/anyhost:1",
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
	invalid := []string{}
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
	invalid := []string{}
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
		srcUri string
		newUri string
	}{
		{"tcp://" + testUserPass + "@localhost:3013", "tcp://localhost:3013"},
		{testUserPass + "@localhost:3013", "localhost:3013"},
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
		t.Run(c.srcUri, func(t *testing.T) {
			newUri, user, pass := connect.ParseCredentialsURI(c.srcUri)
			assert.Equal(t, c.newUri, newUri, "a unexpected new URI")
			assert.Equal(t, testUser, user, "a unexpected username")
			assert.Equal(t, testPass, pass, "a unexpected password")
		})
	}
}

func TestParseCredentialsURI_parseValid(t *testing.T) {
	for _, uri := range validCredentialsUris {
		t.Run(uri, func(t *testing.T) {
			newUri, user, pass := connect.ParseCredentialsURI(uri)
			assert.NotEqual(t, uri, newUri, "URI must change")
			assert.NotEqual(t, "", user, "username must not be empty")
			assert.NotEqual(t, "", pass, "password must not be empty")
		})
	}
}

func TestParseCredentialsURI_notParseInvalid(t *testing.T) {
	invalid := []string{}
	invalid = append(invalid, validBaseUris...)
	invalid = append(invalid, invalidBaseUris...)
	invalid = append(invalid, invalidCredentialsUris...)

	for _, uri := range invalid {
		t.Run(uri, func(t *testing.T) {
			newUri, user, pass := connect.ParseCredentialsURI(uri)
			assert.Equal(t, uri, newUri, "URI must no change")
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
		{"./path/to/socket", connect.UnixNetwork, "./path/to/socket"},
		{"/path/to/socket", connect.UnixNetwork, "/path/to/socket"},
		{"unix:///path/to/socket", connect.UnixNetwork, "/path/to/socket"},
		{"unix://..//path/to/socket", connect.UnixNetwork, "..//path/to/socket"},
		{"..//path", connect.UnixNetwork, "..//path"},
		{"some_uri", connect.TCPNetwork, "some_uri"}, // Keeps unchanged
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

func TestParseUriOpts(t *testing.T) {
	const defaultTimeout = 3 * time.Second

	cases := map[string]struct {
		Url    string
		Opts   connect.UriOpts
		params map[string]string
		Err    string
	}{
		"empty url": {
			Url:  "",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"no scheme": {
			Url:  "host",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"invalid scheme": {
			Url:  ":host",
			Opts: connect.UriOpts{},
			Err:  "missing protocol scheme",
		},
		"no host": {
			Url:  "scheme:///prefix",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"with opaque": {
			Url:  "scheme:host.com/prefix",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		"simple": {
			Url: "scheme://localhost",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with port": {
			Url: "scheme://localhost:3013",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost:3013",
				Host:     "localhost:3013",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"user auth": {
			Url: "scheme://user@localhost",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"user and pass": {
			Url: "scheme://user:pass@localhost",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Password: "pass",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"prefix root": {
			Url: "scheme://localhost/",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with prefix": {
			Url: "scheme://localhost/prefix",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with prefix and fragment": {
			Url: "scheme://localhost/prefix#Fragment",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Tag:      "Fragment",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"only fragment": {
			Url: "scheme://localhost#Fragment",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Tag:      "Fragment",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with param key": {
			Url: "scheme://localhost/prefix?key=anykey",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"key": "anykey"},
			},
			Err: "",
		},
		"with param name": {
			Url: "scheme://localhost/prefix?name=anyname",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"name": "anyname"},
			},
			Err: "",
		},
		"no prefix with params": {
			Url: "scheme://localhost?name=anyname#Fragment",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Tag:      "Fragment",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"name": "anyname"},
			},
			Err: "",
		},
		"with empty param": {
			Url: "scheme://localhost/prefix?name=",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
				Params:   map[string]string{"name": ""},
			},
			Err: "",
		},
		"ssl_key_file": {
			Url: "scheme://localhost?ssl_key_file=/any/kfile",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				KeyFile:  "/any/kfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"ssl_cert_file": {
			Url: "scheme://localhost?ssl_cert_file=/any/certfile",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CertFile: "/any/certfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"ssl_ca_path": {
			Url: "scheme://localhost?ssl_ca_path=/any/capath",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaPath:   "/any/capath",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"ssl_ca_file": {
			Url: "scheme://localhost?ssl_ca_file=/any/cafile",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaFile:   "/any/cafile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"verify peer and host": {
			Url: "scheme://localhost?verify_peer=true&verify_host=true",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"verify peer and host is empty": {
			Url: "scheme://localhost?verify_peer=&verify_host=",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"skip verify peer": {
			Url: "scheme://localhost?verify_peer=false",
			Opts: connect.UriOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipPeerVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		"invalid verify_peer": {
			Url:  "scheme://localhost?verify_peer=asd",
			Opts: connect.UriOpts{},
			Err:  `invalid "verify_peer" param, boolean expected:`,
		},
		"skip verify host": {
			Url: "scheme://localhost?verify_host=false",
			Opts: connect.UriOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipHostVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		"invalid verify_host": {
			Url:  "scheme://localhost?verify_host=asd",
			Opts: connect.UriOpts{},
			Err:  `invalid "verify_host" param, boolean expected:`,
		},
		"timeout": {
			Url: "scheme://localhost?timeout=5.5",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  time.Duration(float64(5.5) * float64(time.Second)),
			},
			Err: "",
		},
		"empty timeout": {
			Url: "scheme://localhost?timeout=",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"invalid timeout": {
			Url:  "scheme://localhost?timeout=asd",
			Opts: connect.UriOpts{},
			Err:  `invalid "timeout" param, float (in seconds) expected:`,
		},
		"full set": {
			Url: "scheme://user:pass@localhost:2012/prefix" +
				"?key=anykey&name=anyname" +
				"&ssl_key_file=kfile&ssl_cert_file=certfile" +
				"&ssl_ca_path=capath&ssl_ca_file=cafile" +
				"&ssl_ciphers=foo:bar:ciphers" +
				"&verify_peer=true&verify_host=false&timeout=2" +
				"#Fragment",
			Opts: connect.UriOpts{
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
				Timeout:        time.Duration(2 * time.Second),
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
			opts, err := connect.CreateUriOpts(tc.Url)
			if tc.Err != "" {
				assert.ErrorContains(t, err, tc.Err)
			} else {
				assert.Equal(t, tc.Opts, opts)
			}
		})
	}
}
