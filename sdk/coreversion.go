package sdk

import "runtime/debug"

// corePath is the module path of the tt core.
const corePath = "github.com/tarantool/tt/v3"

// develVersion is the version Go records for a main module built from a
// working tree it has no version for.
const develVersion = "(devel)"

// CoreVersion returns the version of the tt core the running binary was built
// with, as Go recorded it in the build info: v3.1.0, or a pseudo-version for
// an untagged commit. When the binary is tt itself that is the main module's
// version; otherwise it is the version of the core dependency, or of its
// replacement when the dependency is replaced.
//
// It reports false when the version is unknown: a binary without build
// info, a core built from a working tree ("(devel)"), or a core replaced by
// a local directory.
//
// The core version may later be offered by Services instead.
func CoreVersion() (string, bool) {
	return coreVersion(debug.ReadBuildInfo)
}

// coreVersion is CoreVersion with the build info read by readBuildInfo.
func coreVersion(readBuildInfo func() (*debug.BuildInfo, bool)) (string, bool) {
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return "", false
	}

	if info.Main.Path == corePath {
		return knownVersion(info.Main.Version)
	}

	for _, dep := range info.Deps {
		if dep == nil || dep.Path != corePath {
			continue
		}

		if dep.Replace != nil {
			return knownVersion(dep.Replace.Version)
		}

		return knownVersion(dep.Version)
	}

	return "", false
}

// knownVersion returns version and true, or false when version says nothing.
func knownVersion(version string) (string, bool) {
	if version == "" || version == develVersion {
		return "", false
	}

	return version, true
}
