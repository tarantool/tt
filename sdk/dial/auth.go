package dial

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tarantool/go-tarantool/v3"
)

// Auth is the authentication method of a connection, as the auth_type of
// Tarantool names it. It is tarantool.Auth, named here for a caller that
// picks a method and has no use of go-tarantool otherwise.
type Auth = tarantool.Auth

// The authentication methods.
const (
	// AuthAuto takes the server's method on the SSL transport and chap-sha1
	// on the plain one.
	AuthAuto Auth = tarantool.AutoAuth
	// AuthChapSha1 is chap-sha1, which sends a scramble of the password.
	AuthChapSha1 Auth = tarantool.ChapSha1Auth
	// AuthPapSha256 is pap-sha256, which sends the password as it is, so
	// only on the SSL transport.
	AuthPapSha256 Auth = tarantool.PapSha256Auth
)

const (
	authAuto      = "auto"
	authChapSha1  = "chap-sha1"
	authPapSha256 = "pap-sha256"
)

var (
	// ErrPapSha256Unencrypted is the error of the pap-sha256 method asked for
	// on the plain transport. A pap-sha256 request carries the password
	// itself, so Tarantool's own clients refuse to send it over an
	// unencrypted connection, and so does [New].
	ErrPapSha256Unencrypted = errors.New(
		"authentication method pap-sha256 does not support an unencrypted connection, " +
			"use the ssl transport")
	// ErrServerAuthPapSha256 marks the refusal of the credentials a client
	// sent with chap-sha1, over the plain transport, by a server whose
	// auth_type is pap-sha256: a user whose password was set with pap-sha256
	// can log in with it only, over the ssl transport.
	ErrServerAuthPapSha256 = errors.New(
		"the server authenticates with pap-sha256, which needs the ssl transport")

	errUnknownAuth = errors.New("unknown authentication method")
)

// AuthNames are the names [ParseAuth] takes, "auto" first.
var AuthNames = []string{authAuto, authChapSha1, authPapSha256}

// ParseAuth returns the authentication method name gives, as the auth_type
// parameter of a URI does: "auto" or "" for [AuthAuto], "chap-sha1" or
// "pap-sha256". Any other name is an error. Auth.String gives the name back.
func ParseAuth(name string) (Auth, error) {
	switch name {
	case "", authAuto:
		return AuthAuto, nil
	case authChapSha1:
		return AuthChapSha1, nil
	case authPapSha256:
		return AuthPapSha256, nil
	default:
		return AuthAuto, fmt.Errorf("%w %q, use %s", errUnknownAuth, name,
			strings.Join(AuthNames, ", "))
	}
}

// checkAuth refuses an authentication method [ParseAuth] gives none of.
func checkAuth(auth Auth) error {
	switch auth {
	case AuthAuto, AuthChapSha1, AuthPapSha256:
		return nil
	default:
		return fmt.Errorf("%w: %s", errUnknownAuth, auth)
	}
}
