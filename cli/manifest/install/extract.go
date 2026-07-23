package install

import "strings"

// Path-segment counts for a rock's location under the .rocks/ tree:
// share|lib / tarantool / <dep> is three deep, and the rock-manifest path
// share/tarantool/rocks/<dep> is four.
const (
	rockPathDepth     = 3
	rockManifestDepth = 4
)

// extractMapper builds the per-entry mapper Extract uses for one install. It
// decides, for each archive entry, where it lands under the install-root and
// whether it is written at all:
//
//   - the tt-owned root metadata (manifest, lock, VERSION) and payload files
//     are never laid into the tree — a guest's manifest must not overwrite the
//     project's own, and the metadata is recorded under manifests/ separately;
//   - _runtime/ is written only for a project-scope install that is placing it
//     for the first time (extractRuntime);
//   - a dependency whose reconciled version stays what is already installed is
//     skipped (skipNames);
//   - for the user and system scopes the leading .rocks/ prefix is stripped so
//     files land directly in the shared tree.
func extractMapper(
	scope Scope, skipNames map[string]struct{}, extractRuntime bool,
) func(string) (string, bool) {
	return func(name string) (string, bool) {
		top, rest, _ := strings.Cut(name, "/")

		switch top {
		case runtimeDirName:
			if scope != ScopeProject || !extractRuntime {
				return "", false
			}

			return name, true
		case rocksDirName:
			return mapRocksEntry(scope, rest, skipNames)
		default:
			return "", false
		}
	}
}

// mapRocksEntry maps one entry under the archive's .rocks/ tree. rest is the
// path with the .rocks/ prefix already stripped.
func mapRocksEntry(scope Scope, rest string, skipNames map[string]struct{}) (string, bool) {
	if rest == "" {
		return "", false
	}

	// tt's own install state never travels inside an archive; ignore it if it
	// somehow appears.
	if top, _, _ := strings.Cut(rest, "/"); top == manifestsDirName {
		return "", false
	}

	if dep, ok := rocksDepName(rest); ok {
		if _, skip := skipNames[dep]; skip {
			return "", false
		}
	}

	if scope == ScopeProject {
		return rocksDirName + "/" + rest, true
	}

	// user / system: the shared tree has no .rocks/ level.
	return rest, true
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
