package connect

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
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

// ParseUriOpts parses options from a URI from a URL.
// Accepts default username and password if they are not set in the URL.
func ParseUriOpts(uri *url.URL, username string, password string) (UriOpts, error) {
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
	if p, ok := uri.User.Password(); ok {
		opts.Password = p
	}
	// Q: Do we should keep old logic to check both username and password are empty?
	// ? What if would be required set password from CLI while username from URL?
	if opts.Username == "" && opts.Password == "" {
		opts.Username = username
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
