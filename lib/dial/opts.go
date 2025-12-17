package dial

import "github.com/tarantool/go-tarantool/v2"

// Opts represents set of dialer parameters.
type Opts struct {
	Address         string
	Auth            tarantool.Auth
	User            string
	Password        string
	SslKeyFile      string
	SslCertFile     string
	SslCaFile       string
	SslCiphers      string
	SslPassword     string
	SslPasswordFile string
	Transport       string
}
