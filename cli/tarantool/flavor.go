package tarantool

import (
	"context"
	"os/exec"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/internal/binary"
)

// FlavorCE and FlavorEE are the flavors of a tarantool, Community and
// Enterprise Edition, as a [platform].tarantool constraint names them.
const (
	FlavorCE = "ce"
	FlavorEE = flavorEE
)

// enterpriseMarker is the word Tarantool Enterprise puts in its version banner:
// "Tarantool Enterprise 3.2.0-0-g19607a903" against CE's "Tarantool 3.2.0".
const enterpriseMarker = "Enterprise"

// Flavor returns the flavor of exe, FlavorCE or FlavorEE, or "" when it
// cannot be told: the product tt binary installed it as when it did, and
// what its version banner says otherwise, as for a tarantool from PATH.
func (exe Executable) Flavor(ctx context.Context) string {
	switch exe.Program {
	case binary.ProgramCe:
		return FlavorCE
	case binary.ProgramEe:
		return FlavorEE
	default:
		return DetectFlavor(ctx, exe.Path)
	}
}

// DetectFlavor reports whether the Tarantool at path is a FlavorCE or a
// FlavorEE build, or "" when that cannot be determined.
//
// The flavor has to be read here because the shared version plumbing discards
// it: cmdcontext.TarantoolCli.GetVersion keeps only the last whitespace-
// separated token of the banner, which is the version number, so the
// Enterprise marker never reaches a caller.
func DetectFlavor(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}

	return FlavorFromBanner(string(out))
}

// FlavorFromBanner classifies a Tarantool --version banner as FlavorCE or
// FlavorEE, or "" when it is not a banner this code understands.
func FlavorFromBanner(banner string) string {
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
		return FlavorEE
	}

	return FlavorCE
}
