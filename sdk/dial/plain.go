package dial

import (
	"context"
	"errors"
	"fmt"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool/v3"
)

// plainDialer dials the plain transport and authenticates with chap-sha1,
// the one method Tarantool lets a client use over an unencrypted
// connection: a pap-sha256 request carries the password itself.
//
// A server whose auth_type is pap-sha256 takes chap-sha1 still from a user
// whose password was set before the auth_type changed, and refuses the
// others with a mismatch of the credentials. The error of such a refusal
// wraps [ErrServerAuthPapSha256] too, which says what to do: take the ssl
// transport.
type plainDialer struct {
	address  string
	user     string
	password string
}

// Dial makes plainDialer satisfy the tarantool.Dialer interface.
func (d plainDialer) Dial(ctx context.Context, opts tarantool.DialOpts) (tarantool.Conn, error) {
	// With no user NetDialer reads the greeting and the IPROTO_ID response,
	// which names the auth_type of the server, and authenticates nobody.
	conn, err := tarantool.NetDialer{Address: d.address}.Dial(ctx, opts)
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of go-tarantool says what failed.
	}

	serverAuth := conn.ProtocolInfo().Auth

	authed, err := tarantool.AuthDialer{
		Dialer:   dialedConn{conn: conn},
		Auth:     tarantool.ChapSha1Auth,
		Username: d.user,
		Password: d.password,
	}.Dial(ctx, opts)
	if err != nil {
		if serverAuth == tarantool.PapSha256Auth && isCredsMismatch(err) {
			return nil, fmt.Errorf("%w (%w)", err, ErrServerAuthPapSha256)
		}

		return nil, err //nolint:wrapcheck // The error of go-tarantool says what failed.
	}

	return authed, nil
}

// isCredsMismatch reports whether err is the refusal of the credentials of a
// user by the server.
func isCredsMismatch(err error) bool {
	var serverErr tarantool.ServerError

	return errors.As(err, &serverErr) && serverErr.Code == iproto.ER_CREDS_MISMATCH
}

// dialedConn is a tarantool.Dialer of a connection dialed already: it lets a
// dialer wrapper go on with it.
type dialedConn struct {
	conn tarantool.Conn
}

// Dial returns the connection.
func (d dialedConn) Dial(context.Context, tarantool.DialOpts) (tarantool.Conn, error) {
	return d.conn, nil
}
