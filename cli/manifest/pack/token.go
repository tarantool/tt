package pack

import (
	"runtime"
)

// anyToken is the platform token of a universal archive — one that runs on any
// OS and architecture. Only a --without-deps archive carrying no native file
// earns it.
const anyToken = "any"

// platform is an operating system and CPU architecture pair, spelled the way
// Go spells GOOS and GOARCH. For the four supported pairs its String form is
// the archive's platform token: linux-amd64, linux-arm64, darwin-amd64,
// darwin-arm64.
type platform struct {
	OS   string
	Arch string
}

// String renders the pair as a token, "<os>-<arch>".
func (p platform) String() string {
	return p.OS + "-" + p.Arch
}

// hostPlatform returns the platform of the machine pack runs on, the only one
// it packs for until it learns to build for another.
func hostPlatform() platform {
	return platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// hostToken returns the platform token of the machine pack runs on. Go's GOOS
// and GOARCH names are already the token spelling for the four supported
// pairs, so no translation table is needed.
func hostToken() string {
	return hostPlatform().String()
}

// platformToken picks the token that goes into the archive file name.
//
// An archive is universal only when it carries neither a bundled runtime nor
// any native file: bundling Tarantool pins the archive to a platform because
// the binary is platform-specific, and an ELF or Mach-O file anywhere in the
// archive - a compiled module, an executable from [package].include, a hook's
// output - pins it for the same reason (see nativeFiles). Every other archive
// is per-(os, arch) and takes the host token.
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
