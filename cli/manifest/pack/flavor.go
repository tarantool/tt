package pack

import (
	"context"
	"os/exec"
	"slices"
	"strings"
)

// enterpriseMarker is the word Tarantool Enterprise puts in its version banner:
// "Tarantool Enterprise 3.2.0-0-g19607a903" against CE's "Tarantool 3.2.0".
const enterpriseMarker = "Enterprise"

// DetectTarantoolFlavor reports whether the Tarantool at path is a "ce" or "ee"
// build, or "" when that cannot be determined.
//
// The flavor has to be read here because the shared version plumbing discards
// it: cmdcontext.TarantoolCli.GetVersion keeps only the last whitespace-
// separated token of the banner, which is the version number, so the
// Enterprise marker never reaches a caller. Getting this wrong is not a
// cosmetic error - it decides whether an [ee] manifest gets an EE runtime.
func DetectTarantoolFlavor(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}

	return flavorFromBanner(string(out))
}

// flavorFromBanner classifies a Tarantool --version banner.
func flavorFromBanner(banner string) string {
	first, _, _ := strings.Cut(banner, "\n")

	// A banner is at least "Tarantool <version>"; anything shorter is not a
	// form this code understands, and saying so beats guessing "ce", which
	// would let an unverified build satisfy a [ce] requirement.
	const minBannerFields = 2

	fields := strings.Fields(first)
	if len(fields) < minBannerFields {
		return ""
	}

	if slices.Contains(fields, enterpriseMarker) {
		return flavorEE
	}

	return flavorCE
}
