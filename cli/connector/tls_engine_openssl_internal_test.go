//go:build openssl

package connector

// tlsEngineTimesOutHandshake says that the TLS engine of the build ends a
// handshake the context of the connection ends. The OpenSSL engine does not:
// go-openssl takes the context for the dial of the address only, and a server
// that never answers the handshake holds it until the server closes the
// connection.
const tlsEngineTimesOutHandshake = false
