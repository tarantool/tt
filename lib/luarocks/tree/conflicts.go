package tree

import (
	"fmt"
	"os"
	"strings"
)

// resolveCollision implements upstream luarocks's filename-munging conflict
// resolution: when a deploy-target path is already occupied by a file from
// some prior install (typically a different version of the same rock), the
// new file is written under a munged name composed of the original
// basename and the new install's <name>-<version> string with `.` and `-`
// replaced by `_`. The previously-deployed file is left in place.
//
// Example: deploying metrics 1.5.0-1 to a tree that already holds
// metrics 2.0.0-1's `/share/tarantool/metrics/init.lua` produces
// `/share/tarantool/metrics/init.lua~metrics_1_5_0_1`. The active
// version's bytes stay at the plain path; the inactive sibling is the
// renamed copy.
//
// This is the v1 conservative form: we ALWAYS write inactive (i.e. the
// new install is treated as inactive whenever the target exists). The
// caller — eventually the install/upgrade policy — is responsible for
// promoting a version by swapping bytes between the plain and munged
// paths. (No symlinks; matches upstream Tarantool layout.)
//
// manifest.modules[<name>] is a
// 1-indexed array where [1] is the active version's repo string. We do
// not write that here — the manifest update is the install pipeline's
// concern, on top of the rock_manifest we return from Deploy.
func resolveCollision(target, pkg, ver string) (string, error) {
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return target, nil
		}

		return "", fmt.Errorf("stat %q: %w", target, err)
	}

	suffix := mungedVersion(pkg, ver)
	munged := target + "~" + suffix

	return munged, nil
}

// mungedVersion returns the conflict-suffix form of "<pkg>-<ver>" with
// every `.` and `-` replaced by `_`. This is the canonical munged
// identifier embedded in conflict filenames and used as keys in some
// manifest sub-tables.
func mungedVersion(pkg, ver string) string {
	combined := pkg + "-" + ver
	combined = strings.ReplaceAll(combined, ".", "_")
	combined = strings.ReplaceAll(combined, "-", "_")

	return combined
}

// MungedPath returns the conflict-suffixed sibling of base for the given
// (pkg, ver). Exported because installer logic — selecting the active
// version among munged siblings — needs the same string formation that
// Deploy used.
func MungedPath(base, pkg, ver string) string {
	return base + "~" + mungedVersion(pkg, ver)
}
