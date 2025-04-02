package connect

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	TCPNetwork  = "tcp"
	UnixNetwork = "unix"
)

const (
	// userPathRe is a regexp for a username:password pair.
	userpassRe = `[^@:/]+:[^@:/]+`

	// uriPathPrefixRe is a regexp for a path prefix in uri, such as `scheme://path``.
	uriPathPrefixRe = `((~?/+)|((../+)*))?`

	// systemPathPrefixRe is a regexp for a path prefix to use without scheme.
	systemPathPrefixRe = `(([\.~]?/+)|((../+)+))`

	// timeoutParam is a URL parameter with timeout value (in seconds).
	// Timeout is used both as connection timeout and calls timeout.
	timeoutParam = "timeout"

	// sslKeyFileParam is a URL parameter with SSL key file path.
	sslKeyFileParam = "ssl_key_file"

	// sslCertFileParam is a URL parameter with SSL cert file path.
	sslCertFileParam = "ssl_cert_file"

	// sslCaFileParam is a URL parameter with SSL CA file path.
	sslCaFileParam = "ssl_ca_file"

	// sslCaPathParam is a URL parameter with SSL CA directory path.
	sslCaPathParam = "ssl_ca_path"

	// sslCiphersParam is a URL parameter with SSL ciphers.
	sslCiphersParam = "ssl_ciphers"

	// verifyHostParam is a URL parameter defining whether to verify
	// certificate’s name against the host.
	verifyHostParam = "verify_host"

	// verifyPeerParam is a URL parameter defining whether to verify
	// peer’s SSL certificate.
	verifyPeerParam = "verify_peer"

	// defaultVerifyHostParam is the default VerifyHostParam.
	defaultVerifyHostParam = true

	// defaultVerifyPeerParam is the default VerifyPeerParam.
	defaultVerifyPeerParam = true

	// defaultTimeoutParam is a default TimeoutParam.
	defaultTimeoutParam = 3 * time.Second
)

// UriOpts is a universal list of connect options retrieved from an URI.
type UriOpts struct {
	// Endpoint is a an endpoint to connect: [scheme://]host[:port].
	Endpoint string
	// Host is a an address to connect: host[:port].
	Host string
	// Prefix is a configuration prefix.
	Prefix string
	// Tag value of #fragment URL part.
	Tag string
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
	// Params keeps extra URL parameters.
	Params map[string]string
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
	// https://user:password@host:port
	// https://user:password@host
	httpsReStr := `(http|https)://` + userpassRe + `@([\w\.-]+(:\d+)?)(/[\w\-\./~]*)?`

	uriReStr := "^((" + tcpReStr + ")|(" + httpsReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
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
		network = UnixNetwork
		address = uri
	case uriLen >= 7 && uri[0:7] == "unix://":
		network = UnixNetwork
		address = uri[7:]
	case uriLen >= 6 && uri[0:6] == "tcp://":
		network = TCPNetwork
		address = uri[6:]
	default:
		network = TCPNetwork
		address = uri
	}

	// In the case of a complex uri, shell expansion does not occur, so do it manually.
	if network == UnixNetwork &&
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

func getBooleanParam(param string, defaultValue bool) (bool, error) {
	if param == "" {
		return defaultValue, nil
	}

	param = strings.ToLower(param)
	return strconv.ParseBool(param)
}

func getDurationParam(param string, defaultValue time.Duration) (time.Duration, error) {
	if param == "" {
		return defaultValue, nil
	}

	seconds, err := strconv.ParseFloat(param, 64)
	if err != nil {
		return defaultValue, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// parseUriOpts extract options from a URL to UriOpts.
func parseUriOpts(uri *url.URL) (UriOpts, error) {
	var err error
	endpoint := url.URL{
		Scheme: uri.Scheme,
		Host:   uri.Host,
	}
	opts := UriOpts{
		Endpoint: endpoint.String(),
		Host:     uri.Host,
		Prefix:   uri.Path,
		Tag:      uri.Fragment,
		Username: uri.User.Username(),
		Timeout:  defaultTimeoutParam,
		Params:   make(map[string]string),
	}
	if password, ok := uri.User.Password(); ok {
		opts.Password = password
	}

	values := uri.Query()
	for k, v := range values {
		switch k {
		case sslKeyFileParam:
			opts.KeyFile = v[0]
		case sslCertFileParam:
			opts.CertFile = v[0]
		case sslCaPathParam:
			opts.CaPath = v[0]
		case sslCaFileParam:
			opts.CaFile = v[0]
		case sslCiphersParam:
			opts.Ciphers = v[0]

		case timeoutParam:
			opts.Timeout, err = getDurationParam(v[0], defaultTimeoutParam)
			if err != nil {
				return opts, fmt.Errorf("invalid %q param, float (in seconds) expected: %s",
					k, err)
			}

		case verifyHostParam:
			verify, err := getBooleanParam(v[0], defaultVerifyHostParam)
			if err != nil {
				return opts, fmt.Errorf("invalid %q param, boolean expected: %s", k, err)
			}
			opts.SkipHostVerify = !verify

		case verifyPeerParam:
			verify, err := getBooleanParam(v[0], defaultVerifyHostParam)
			if err != nil {
				return opts, fmt.Errorf("invalid %q param, boolean expected: %s", k, err)
			}
			opts.SkipPeerVerify = !verify

		default:
			opts.Params[k] = v[0]
		}
	}

	return opts, nil
}

// parseUrl returns a URL, nil if string could be recognized as a URL,
// otherwise nil, an error.
func parseUrl(str string) (*url.URL, error) {
	uri, err := url.Parse(str)
	// The URL general form represented is:
	// [scheme:][//[userinfo@]host][/]path[?query][#fragment]
	// URLs that do not start with a slash after the scheme are interpreted as:
	// scheme:opaque[?query][#fragment]
	//
	// So it is enough to check scheme, host and opaque to avoid to handle
	// app:instance as a URL.
	if err != nil {
		return nil, err
	}
	if uri.Scheme == "" || uri.Host == "" {
		return nil, errors.New("URL must contain the scheme and the host parts")
	}
	return uri, nil
}

// CreateUriOpts parse URL string and creates appropriated UriOpts.
func CreateUriOpts(url string) (UriOpts, error) {
	uri, err := parseUrl(url)
	if err != nil {
		return UriOpts{}, err
	}

	return parseUriOpts(uri)
}
