// Package dial builds the go-tarantool dialers tt connects to Tarantool
// with, for the plain and the SSL transports.
//
// [New] returns a tarantool.Dialer for [Opts]: the address, the
// credentials, the authentication method, the SSL files and the transport.
// An empty transport is SSL when any SSL option is set and plain otherwise;
// [Parse] reads a transport name, as the transport parameter of a URI gives
// it, and [ParseAuth] an authentication method, as its auth_type parameter
// does. The SSL dialer runs on the Go implementation of TLS, and on OpenSSL
// in a build with the openssl tag.
//
// The authentication method is chap-sha1 or pap-sha256, as the auth_type of
// Tarantool is. The automatic one is the server's on the SSL transport and
// chap-sha1 on the plain transport, which refuses pap-sha256: its request
// carries the password as it is, and Tarantool's own clients do not send
// one over an unencrypted connection either.
//
// The package takes the options as they are: parsing a URI and choosing
// the credentials is the caller's work, with
// [github.com/tarantool/tt/sdk/connect]:
//
//	uri, user, password := connect.ParseCredentialsURI(target)
//	base, params, err := connect.SplitURIQuery(uri)
//	network, address := connect.ParseBaseURI(base)
//	auth, err := dial.ParseAuth(params.Get("auth_type"))
//	dialer, err := dial.New(dial.Opts{
//		Address:   network + "://" + address,
//		Auth:      auth,
//		User:      user,
//		Password:  password,
//		SslCaFile: params.Get("ssl_ca_file"),
//		Transport: params.Get("transport"),
//	})
//
// The API of this package is alpha and may change incompatibly.
package dial
