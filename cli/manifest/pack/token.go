package pack

import (
	"runtime"
)

// anyToken is the platform token of a universal archive — one that runs on any
// OS and architecture. Only a --without-deps archive of a product with no
// native components earns it.
const anyToken = "any"

// hostToken returns the platform token of the machine pack runs on:
// "<os>-<arch>", e.g. linux-amd64, linux-arm64, darwin-amd64, darwin-arm64.
// Go's GOARCH names already match the RFC's token vocabulary for the four
// supported pairs, so no translation table is needed.
func hostToken() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// platformToken picks the token that goes into the archive file name.
//
// An archive is universal only when it carries neither a bundled runtime nor
// any native artifact: bundling Tarantool pins the archive to a platform
// because the binary is platform-specific, and a .so pins it for the same
// reason. Every other archive is per-(os, arch) and takes the host token.
func platformToken(withDeps, hasNative bool) string {
	if withDeps || hasNative {
		return hostToken()
	}

	return anyToken
}

// archiveName builds the archive file name: <package>-<version>-<token>.tt.
func archiveName(pkg, version, token string) string {
	return pkg + "-" + version + "-" + token + archiveExt
}
