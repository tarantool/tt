//go:build tt_ssl_disable

package dial

import (
	"errors"

	"github.com/tarantool/go-tarantool/v2"
)

var (
	errSSLSupportIsDisabled = errors.New("SSL support is disabled")
)

func ssl(opts Opts) (tarantool.Dialer, error) {
	return nil, errSSLSupportIsDisabled
}
