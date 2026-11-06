// Package dial builds the go-tarantool dialers tt connects to Tarantool
// with, for the plain and the SSL transports.
//
// [New] returns a tarantool.Dialer for [Opts]: the address, the
// credentials, the SSL files and the transport. An empty transport is SSL
// when any SSL option is set and plain otherwise; [Parse] reads a
// transport name, as the transport parameter of a URI gives it. The SSL
// dialer runs on the Go implementation of TLS, and on OpenSSL in a build
// with the openssl tag.
//
// The package takes the options as they are: parsing a URI and choosing
// the credentials is the caller's work, with
// [github.com/tarantool/tt/sdk/connect]:
//
//	uri, user, password := connect.ParseCredentialsURI(target)
//	base, params, err := connect.SplitURIQuery(uri)
//	network, address := connect.ParseBaseURI(base)
//	dialer, err := dial.New(dial.Opts{
//		Address:   network + "://" + address,
//		User:      user,
//		Password:  password,
//		SslCaFile: params.Get("ssl_ca_file"),
//		Transport: params.Get("transport"),
//	})
//
// The API of this package is alpha and may change incompatibly.
package dial
