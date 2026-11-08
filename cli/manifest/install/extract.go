package install

import (
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// extractMapper builds the per-entry mapper Extract uses for one install into
// the project scope, for what is not a rock. It decides, for each archive
// entry, where it lands under the install-root and whether it is written at
// all:
//
//   - the tt-owned root metadata (manifest, lock, VERSION) and payload files
//     are never laid into the tree — a guest's manifest must not overwrite the
//     project's own, and the metadata is recorded under manifests/ separately;
//   - _runtime/ is not laid into the tree at all: its components are installed
//     into the project's .tarantool/versions/ (see placeRuntimes);
//   - .templates/ is written as is, the install-root being the application
//     directory; checkTemplates has already refused an install that would lay
//     it over an existing one;
//   - .rocks/ is grafted into the project's tree rock by rock (graftArchive),
//     not extracted over it.
func extractMapper() func(string) (string, bool) {
	return func(name string) (string, bool) {
		top, _, _ := strings.Cut(name, "/")
		if top == templatesDirName {
			return name, true
		}

		return "", false
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
