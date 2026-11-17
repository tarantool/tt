//go:build !openssl

package connector

// tlsEngineTimesOutHandshake says that the TLS engine of the build ends a
// handshake the context of the connection ends. The gostls engine does.
const tlsEngineTimesOutHandshake = true
