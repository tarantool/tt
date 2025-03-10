package connect

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tarantool/tt/cli/connector"
)

const (
	// userPathRe is a regexp for a username:password pair.
	userpassRe = `[^@:/]+:[^@:/]+`

	// uriPathPrefixRe is a regexp for a path prefix in uri, such as `scheme://path``.
	uriPathPrefixRe = `((~?/+)|((../+)*))?`

	// systemPathPrefixRe is a regexp for a path prefix to use without scheme.
	systemPathPrefixRe = `(([\.~]?/+)|((../+)+))`

	DefaultUriTimeout = 3 * time.Second
)

// UriOpts is a universal list of connect options retrieved from an URI.
type UriOpts struct {
	// Endpoint is a an endpoint to connect: [scheme://]host[:port].
	Endpoint string
	// Host is a an address to connect: host[:port].
	Host string
	// Prefix is a configuration prefix.
	Prefix string
	// Key is a target key.
	Key string
	// Instance is an instance name.
	Instance string
	// Username is a user name for authorization
	Username string
	// Password is a password for authorization
	Password string
	// KeyFile is a path to a private SSL key file.
	KeyFile string
	// CertFile is a path to an SSL certificate file.
	CertFile string
	// CaPath is a path to a trusted certificate authorities (CA) directory.
	CaPath string
	// CaFile is a path to a trusted certificate authorities (CA) file.
	CaFile string
	// Ciphers is a colon-separated (:) list of SSL cipher suites the
	// connection can use.
	Ciphers string
	// SkipHostVerify controls whether a client verifies the server's
	// host name. This is dangerous option so by default it is false.
	SkipHostVerify bool
	// SkipHostVerify controls whether a client verifies the server's
	// certificate chain. This is dangerous option so by default it is false.
	SkipPeerVerify bool
	// Timeout is a timeout for actions.
	Timeout time.Duration
}

// IsBaseURI returns true if a string is a valid URI.
func IsBaseURI(str string) bool {
	// tcp://host:port
	// host:port
	tcpReStr := `(tcp://)?([\w\\.-]+:\d+)`
	// unix://../path
	// unix://~/path
	// unix:///path
	// unix://path
	unixReStr := `unix://` + uriPathPrefixRe + `[^\./@]+[^@]*`
	// ../path
	// ~/path
	// /path
	// ./path
	pathReStr := systemPathPrefixRe + `[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
}

// IsCredentialsURI returns true if a string is a valid credentials URI.
func IsCredentialsURI(str string) bool {
	// tcp://user:password@host:port
	// user:password@host:port
	tcpReStr := `(tcp://)?` + userpassRe + `@([\w\.-]+:\d+)`
	// unix://user:password@../path
	// unix://user:password@~/path
	// unix://user:password@/path
	// unix://user:password@path
	unixReStr := `unix://` + userpassRe + `@` + uriPathPrefixRe + `[^\./@]+.*`
	// user:password@../path
	// user:password@~/path
	// user:password@/path
	// user:password@./path
	pathReStr := userpassRe + `@` + systemPathPrefixRe + `[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
}

// ParseBaseURI parses an URI and returns:
// (network, address)
func ParseBaseURI(uri string) (string, string) {
	var network, address string
	uriLen := len(uri)

	switch {
	case uriLen > 0 && (uri[0] == '.' || uri[0] == '/' || uri[0] == '~'):
		network = connector.UnixNetwork
		address = uri
	case uriLen >= 7 && uri[0:7] == "unix://":
		network = connector.UnixNetwork
		address = uri[7:]
	case uriLen >= 6 && uri[0:6] == "tcp://":
		network = connector.TCPNetwork
		address = uri[6:]
	default:
		network = connector.TCPNetwork
		address = uri
	}

	// In the case of a complex uri, shell expansion does not occur, so do it manually.
	if network == connector.UnixNetwork &&
		strings.HasPrefix(address, "~/") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			address = filepath.Join(homeDir, address[2:])
		}
	}

	return network, address
}

// ParseCredentialsURI parses a URI with credentials and returns:
// (URI without credentials, user, password)
func ParseCredentialsURI(str string) (string, string, string) {
	if !IsCredentialsURI(str) {
		return str, "", ""
	}

	re := regexp.MustCompile(userpassRe + `@`)
	// Split the string into two parts by credentials to create a string
	// without the credentials.
	split := re.Split(str, 2)
	newStr := split[0] + split[1]

	// Parse credentials.
	credentialsStr := re.FindString(str)
	credentialsLen := len(credentialsStr) - 1 // We don't need a last '@'.
	credentialsSlice := strings.Split(credentialsStr[:credentialsLen], ":")

	return newStr, credentialsSlice[0], credentialsSlice[1]
}

// ParseUriOpts parses options from a URI from a URL.
func ParseUriOpts(uri *url.URL) (UriOpts, error) {
	if uri.Scheme == "" || uri.Host == "" {
		return UriOpts{},
			fmt.Errorf("URL must contain the scheme and the host parts")
	}

	endpoint := url.URL{
		Scheme: uri.Scheme,
		Host:   uri.Host,
	}
	values := uri.Query()
	opts := UriOpts{
		Endpoint: endpoint.String(),
		Host:     uri.Host,
		Prefix:   uri.Path,
		Key:      values.Get("key"),
		Instance: values.Get("name"),
		Username: uri.User.Username(),
		KeyFile:  values.Get("ssl_key_file"),
		CertFile: values.Get("ssl_cert_file"),
		CaPath:   values.Get("ssl_ca_path"),
		CaFile:   values.Get("ssl_ca_file"),
		Ciphers:  values.Get("ssl_ciphers"),
		Timeout:  DefaultUriTimeout,
	}
	if password, ok := uri.User.Password(); ok {
		opts.Password = password
	}

	verifyPeerStr := values.Get("verify_peer")
	verifyHostStr := values.Get("verify_host")
	timeoutStr := values.Get("timeout")

	if verifyPeerStr != "" {
		verifyPeerStr = strings.ToLower(verifyPeerStr)
		if verify, err := strconv.ParseBool(verifyPeerStr); err == nil {
			if !verify {
				opts.SkipPeerVerify = true
			}
		} else {
			err = fmt.Errorf("invalid verify_peer, boolean expected: %w", err)
			return opts, err
		}
	}

	if verifyHostStr != "" {
		verifyHostStr = strings.ToLower(verifyHostStr)
		if verify, err := strconv.ParseBool(verifyHostStr); err == nil {
			if !verify {
				opts.SkipHostVerify = true
			}
		} else {
			err = fmt.Errorf("invalid verify_host, boolean expected: %w", err)
			return opts, err
		}
	}

	if timeoutStr != "" {
		if timeout, err := strconv.ParseFloat(timeoutStr, 64); err == nil {
			opts.Timeout = time.Duration(timeout * float64(time.Second))
		} else {
			err = fmt.Errorf("invalid timeout, float expected: %w", err)
			return opts, err
		}
	}

	return opts, nil
}
