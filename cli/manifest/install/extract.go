package install

import (
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// Path-segment counts for a rock's location under the .rocks/ tree:
// share|lib / tarantool / <dep> is three deep, and the rock-manifest path
// share/tarantool/rocks/<dep> is four.
const (
	rockPathDepth     = 3
	rockManifestDepth = 4
)

// extractMapper builds the per-entry mapper Extract uses for one install into
// the project scope. It decides, for each archive entry, where it lands under
// the install-root and whether it is written at all:
//
//   - the tt-owned root metadata (manifest, lock, VERSION) and payload files
//     are never laid into the tree — a guest's manifest must not overwrite the
//     project's own, and the metadata is recorded under manifests/ separately;
//   - _runtime/ is not laid into the tree at all: its components are installed
//     into the project's .tarantool/versions/ (see placeRuntimes);
//   - .templates/ is written as is, the install-root being the application
//     directory; checkTemplates has already refused an install that would lay
//     it over an existing one;
//   - a dependency whose reconciled version stays what is already installed is
//     skipped (skipNames).
func extractMapper(skipNames map[string]struct{}) func(string) (string, bool) {
	return func(name string) (string, bool) {
		top, rest, _ := strings.Cut(name, "/")

		switch top {
		case templatesDirName:
			return name, true
		case state.RocksDirName:
			return mapRocksEntry(rest, skipNames)
		default:
			return "", false
		}
	}
}

// versionMapper builds the mapper Extract uses for a version installed into
// the user or system scope. The version directory is the package's alone, so
// it receives the archive whole: the manifest, the lock and VERSION at its
// top, the payload and license files next to them, .rocks/ and .templates/.
// That makes it the install root of a project, which is what tt instance and
// the tarantool resolver read an installed version as, and it keeps every
// file the package shipped where the package expects it — a relative
// ./bin/<tool> of an instance description resolves from the application
// directory.
//
// Only _runtime/ is left out: the runtimes it bundles are installed next to
// the products of tt binary (see placeRuntimes), not inside the version. tt's
// own install state never travels in an archive and is dropped if it does.
func versionMapper() func(string) (string, bool) {
	return func(name string) (string, bool) {
		top, rest, _ := strings.Cut(name, "/")

		switch top {
		case runtimeDirName:
			return "", false
		case state.RocksDirName:
			if first, _, _ := strings.Cut(rest, "/"); first == state.ManifestsDirName {
				return "", false
			}

			return name, true
		default:
			return name, true
		}
	}
}

// mapRocksEntry maps one entry under the archive's .rocks/ tree into the
// project's. rest is the path with the .rocks/ prefix already stripped.
func mapRocksEntry(rest string, skipNames map[string]struct{}) (string, bool) {
	if rest == "" {
		return "", false
	}

	// tt's own install state never travels inside an archive; ignore it if it
	// somehow appears.
	if top, _, _ := strings.Cut(rest, "/"); top == state.ManifestsDirName {
		return "", false
	}

	if dep, ok := rocksDepName(rest); ok {
		if _, skip := skipNames[dep]; skip {
			return "", false
		}
	}

	return state.RocksDirName + "/" + rest, true
}

// rocksDepName extracts the rock name owning a path under the .rocks/ tree:
// share/tarantool/<dep>/…, lib/tarantool/<dep>/…, or the rock-manifest path
// share/tarantool/rocks/<dep>/<version>/…. It returns false for anything that
// is not inside a named rock's subtree.
func rocksDepName(rest string) (string, bool) {
	parts := strings.Split(rest, "/")
	if len(parts) < rockPathDepth {
		return "", false
	}

	if parts[1] != "tarantool" || (parts[0] != "share" && parts[0] != "lib") {
		return "", false
	}

	if parts[2] == "rocks" {
		if len(parts) < rockManifestDepth {
			return "", false
		}

		return parts[3], true
	}

	return parts[2], true
}
