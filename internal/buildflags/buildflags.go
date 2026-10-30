// Package buildflags is the one place the build tags and linker flags of a
// tt binary are spelled out. The magefile builds tt with them and
// tt binary build tt builds a distribution of tt with them, so the two
// cannot drift apart.
package buildflags

import (
	"errors"
	"fmt"
)

// Env is the environment variable that selects the TLS implementation tt is
// built with.
const Env = "TT_CLI_BUILD_SSL"

// SSL is the TLS implementation a tt binary is built with.
type SSL string

// The TLS implementations.
const (
	// Default is TLS in pure Go: the binary needs no OpenSSL and no cgo.
	Default SSL = "default"
	// OpenSSL links the OpenSSL of the machine the binary runs on.
	OpenSSL SSL = "openssl"
	// OpenSSLStatic links OpenSSL into the binary, which is static.
	OpenSSLStatic SSL = "openssl_static"
)

// ErrUnsupported reports a TLS implementation tt cannot be built with.
var ErrUnsupported = errors.New("unsupported build type")

// baseTags make the binary resolve names and users in Go, without the NSS
// modules of glibc.
var baseTags = []string{"netgo", "osusergo"}

// staticLdflags link a cgo binary statically through the external linker.
var staticLdflags = []string{"-linkmode=external", "-extldflags", "-static"}

// Parse returns the TLS implementation named name. The empty name is
// Default.
func Parse(name string) (SSL, error) {
	switch SSL(name) {
	case "", Default:
		return Default, nil
	case OpenSSL, OpenSSLStatic:
		return SSL(name), nil
	default:
		return "", fmt.Errorf("%w: %q, supported: %s, %s, %s",
			ErrUnsupported, name, Default, OpenSSL, OpenSSLStatic)
	}
}

// Tags returns the build tags of a binary built with s.
func (s SSL) Tags() []string {
	tags := append([]string(nil), baseTags...)

	switch s {
	case Default:
	case OpenSSL:
		tags = append(tags, "openssl")
	case OpenSSLStatic:
		tags = append(tags, "openssl", "openssl_static")
	}

	return tags
}

// Ldflags returns the linker flags a binary built with s for goos needs:
// the static link of OpenSSLStatic everywhere but on macOS, which has no
// static libc.
func (s SSL) Ldflags(goos string) []string {
	if s == OpenSSLStatic && goos != "darwin" {
		return append([]string(nil), staticLdflags...)
	}

	return nil
}

// Cgo reports whether a binary built with s needs cgo.
func (s SSL) Cgo() bool {
	return s == OpenSSL || s == OpenSSLStatic
}
