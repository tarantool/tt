//go:build tt_ssl_disable
// +build tt_ssl_disable

package dial

import (
	"errors"

	"github.com/tarantool/go-tarantool/v2"
)

func ssl(opts Opts) (tarantool.Dialer, error) {
	return nil, errors.New("SSL support is disabled")
}
