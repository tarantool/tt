package connect_test

import (
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/connect"
)

// spell-checker:ignore kfile

const (
	testUser     = "a-фs$d!e%*1#2?3&44"
	testPass     = "bb-фs$d!e%*1#2?3&666" //nolint:gosec // A fixture, not a secret.
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
	"unix/:/path/to/file",
	"unix/:./path/to/file",
	"unix/:../path/to/file",
	"unix/:~/file",
	"unix/:/path/to/file?transport=plain",
	"localhost:3301?transport=ssl",
	"localhost:3301?transport=ssl&ssl_ca_file=/x",
	"localhost:3301?ssl_ca_file=/etc/ssl/a@b",
	"localhost:3301?",
	"127.0.0.1:3301?transport=ssl&ssl_ca_file=ca.pem&ssl_ciphers=ECDHE:AES256",
	"[::1]:3301?transport=ssl",
	"tcp://localhost:3301?transport=ssl&ssl_key_file=k.pem",
	"tcp://[::1]:3301?ssl_cert_file=c.pem",
	"unix://path?transport=plain",
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
	testUserPass + "@localhost:3301?transport=ssl",
	testUserPass + "@localhost:3301?transport=ssl&ssl_ca_file=/x",
	testUserPass + "@127.0.0.1:3301?ssl_ca_file=ca.pem&ssl_ciphers=ECDHE:AES256",
	testUserPass + "@[::1]:3301?transport=ssl",
	"tcp://" + testUserPass + "@localhost:3301?transport=ssl&ssl_key_file=k.pem",
	"tcp://" + testUserPass + "@[::1]:3301?ssl_ca_file=/etc/ssl/a@b",
	"unix://" + testUserPass + "@path?transport=plain",
	"user:secretpw@127.0.0.1:3311?transport=ssl&ssl_ca_file=/x",
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
	"localhost?transport=ssl",
	"localhost:asd?transport=ssl",
	"?transport=ssl",
	":3301?transport=ssl",
	"tcp:/localhost:3301?transport=ssl",
	"asd://localhost:3301?transport=ssl",
	"unix/:?transport=ssl",
	"unix/:2?transport=ssl",
	"unix/:/?transport=ssl",
	"unix//:asd?transport=ssl",
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
	"user@localhost:3301?transport=ssl",
	"tcp://user@localhost:3301?transport=ssl",
	"user:password@localhost?transport=ssl",
	"user:password@localhost:asd?transport=ssl",
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
			assert.NotEmpty(t, user, "username must not be empty")
			assert.NotEmpty(t, pass, "password must not be empty")
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
			assert.Empty(t, user, "username must be empty")
			assert.Empty(t, pass, "password must be empty")
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
		{"unix/:/path/to/socket", connect.UnixNetwork, "/path/to/socket"},
		{"unix/:./path/to/socket", connect.UnixNetwork, "./path/to/socket"},
		{"unix/:../path/to/socket", connect.UnixNetwork, "../path/to/socket"},
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

func TestSplitURIQuery(t *testing.T) {
	cases := []struct {
		uri    string
		base   string
		params map[string][]string
	}{
		{uri: "localhost:3301", base: "localhost:3301"},
		{uri: "localhost:3301?", base: "localhost:3301", params: map[string][]string{}},
		{
			uri:    "localhost:3301?transport=ssl&ssl_ca_file=%2Fx",
			base:   "localhost:3301",
			params: map[string][]string{"transport": {"ssl"}, "ssl_ca_file": {"/x"}},
		},
		{
			uri:    "tcp://[::1]:3301?ssl_ciphers=ECDHE:AES256",
			base:   "tcp://[::1]:3301",
			params: map[string][]string{"ssl_ciphers": {"ECDHE:AES256"}},
		},
		{
			uri:    "unix/:/tmp/s.sock?transport=plain",
			base:   "unix/:/tmp/s.sock",
			params: map[string][]string{"transport": {"plain"}},
		},
		// The path forms keep a "?": it is a part of the path.
		{uri: "/tmp/s?.sock", base: "/tmp/s?.sock"},
		{uri: "./s?a=b", base: "./s?a=b"},
		{uri: "~/s?a=b", base: "~/s?a=b"},
		{uri: "../s?a=b", base: "../s?a=b"},
		{uri: "unix://path?a=b", base: "unix://path?a=b"},
		{uri: "", base: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.uri, func(t *testing.T) {
			base, params, err := connect.SplitURIQuery(testCase.uri)
			require.NoError(t, err)
			assert.Equal(t, testCase.base, base)

			if testCase.params == nil {
				assert.Nil(t, params)
			} else {
				assert.Equal(t, url.Values(testCase.params), params)
			}
		})
	}
}

func TestSplitURIQueryMalformedDoesNotQuoteQuery(t *testing.T) {
	_, _, err := connect.SplitURIQuery("localhost:3301?ssl_password=secretpw%zz&a;b")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secretpw")
}

func TestRedactCredentials(t *testing.T) {
	//nolint:gosec // Fixtures, not credentials.
	cases := map[string]string{
		"":                                    "",
		"localhost:3301":                      "localhost:3301",
		"app:inst":                            "app:inst",
		"user:secretpw@localhost:3301":        "[REDACTED]@localhost:3301",
		"secretpw@localhost:3301?a=b":         "[REDACTED]@localhost:3301?a=b",
		"tcp://user:secretpw@localhost:3301":  "tcp://[REDACTED]@localhost:3301",
		"https://token@host/prefix":           "https://[REDACTED]@host/prefix",
		"user:sec/ret:pw@host:3301":           "[REDACTED]@host:3301",
		"user:sec@ret@host:3301":              "[REDACTED]@host:3301",
		"unix://user:secretpw@/run/s.sock":    "unix://[REDACTED]@/run/s.sock",
		"user:secretpw@127.0.0.1:3311?a=/x":   "[REDACTED]@127.0.0.1:3311?a=/x",
		"host:3301?ssl_ca_file=/etc/ssl/a@b":  "[REDACTED]@b",
		"secretpw@127.0.0.1:3311?transport=s": "[REDACTED]@127.0.0.1:3311?transport=s",
	}

	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, connect.RedactCredentials(in))
		})
	}
}

func TestCreateURIOptsErrorDoesNotLeakPassword(t *testing.T) {
	for _, uri := range []string{
		"https://user:secretpw@localhost:badport/prefix",
		"https://user:secretpw@local host:2379",
		"https://user:secretpw@localhost:2379/%zz",
		"https://user:secretpw@localhost:2379/\x7f",
	} {
		t.Run(uri, func(t *testing.T) {
			_, err := connect.CreateURIOpts(uri)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secretpw")
		})
	}

	// Not an error of the parser, but of the shape.
	_, err := connect.CreateURIOpts("user:secretpw@localhost:3301")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secretpw")
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
				Scheme:   "scheme",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"with port": {
			URL: "scheme://localhost:3013",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost:3013",
				Scheme:   "scheme",
				Host:     "localhost:3013",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"user auth": {
			URL: "scheme://user@localhost",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Scheme:   "scheme",
				Host:     "localhost",
				Username: "user",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"user and pass": { //nolint:gosec // A fixture, not a secret.
			URL: "scheme://user:pass@localhost",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
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
				Scheme:   "scheme",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"verify peer and host is empty": {
			URL: "scheme://localhost?verify_peer=&verify_host=",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Scheme:   "scheme",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		"skip verify peer": {
			URL: "scheme://localhost?verify_peer=false",
			Opts: connect.URIOpts{
				Endpoint:       "scheme://localhost",
				Scheme:         "scheme",
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
				Scheme:         "scheme",
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
				Scheme:   "scheme",
				Host:     "localhost",
				Timeout:  time.Duration(float64(5.5) * float64(time.Second)),
			},
			Err: "",
		},
		"empty timeout": {
			URL: "scheme://localhost?timeout=",
			Opts: connect.URIOpts{
				Endpoint: "scheme://localhost",
				Scheme:   "scheme",
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
				Scheme:         "scheme",
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

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if testCase.Opts.Params == nil {
				testCase.Opts.Params = make(map[string]string)
			}

			opts, err := connect.CreateURIOpts(testCase.URL)
			if testCase.Err != "" {
				assert.ErrorContains(t, err, testCase.Err)
			} else {
				assert.Equal(t, testCase.Opts, opts)
			}
		})
	}
}

func TestCreateURIOptsScheme(t *testing.T) {
	for _, scheme := range []string{"https", "http", "tcp"} {
		t.Run(scheme, func(t *testing.T) {
			opts, err := connect.CreateURIOpts(scheme + "://user:pass@localhost:2379/prefix" +
				"?ssl_ca_file=ca.pem&verify_host=false#key")
			require.NoError(t, err)
			assert.Equal(t, scheme, opts.Scheme)
			assert.Equal(t, scheme+"://localhost:2379", opts.Endpoint)
			assert.Equal(t, "ca.pem", opts.CaFile)
			assert.True(t, opts.SkipHostVerify)
			assert.False(t, opts.SkipPeerVerify)
		})
	}
}
