package pack

import (
	"context"

	"github.com/tarantool/tt/v3/cli/tarantool"
)

// DetectTarantoolFlavor reports whether the Tarantool at path is a "ce" or "ee"
// build, or "" when that cannot be determined (tarantool.DetectFlavor).
// Getting this wrong is not a cosmetic error - it decides whether an [ee]
// manifest gets an EE runtime.
func DetectTarantoolFlavor(ctx context.Context, path string) string {
	return tarantool.DetectFlavor(ctx, path)
}

// FlavorFromBanner classifies a Tarantool --version banner as "ce" or "ee",
// or "" when it is not a banner this code understands
// (tarantool.FlavorFromBanner).
func FlavorFromBanner(banner string) string {
	return tarantool.FlavorFromBanner(banner)
}
