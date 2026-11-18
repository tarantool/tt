package pack

import (
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// anyToken is the platform token of a universal archive — one that runs on any
// OS and architecture. Only a --without-deps archive carrying no native file
// earns it.
const anyToken = "any"

// hostToken returns the platform token of the machine pack runs on. Go's GOOS
// and GOARCH names are already the token spelling for the four supported
// pairs, so no translation table is needed.
func hostToken() string {
	return native.Host().String()
}

// platformToken picks the token that goes into the archive file name: that
// of target, the platform the package was built for (build.Result.Target).
//
// An archive is universal only when it carries neither a bundled runtime nor
// any native file: bundling Tarantool pins the archive to a platform because
// the binary is platform-specific, and an ELF or Mach-O file anywhere in the
// archive - a compiled module, an executable from [package].include, a hook's
// output - pins it for the same reason (see native.Files). Every other archive
// is per-(os, arch) and takes the token of target.
func platformToken(target native.Platform, withDeps, hasNative bool) string {
	if withDeps || hasNative {
		return target.String()
	}

	return anyToken
}

// archiveName builds the archive file name: <package>-<version>-<token>.tt.
func archiveName(pkg, version, token string) string {
	return pkg + "-" + version + "-" + token + archiveExt
}
