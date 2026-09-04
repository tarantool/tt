package scaffold

import (
	"fmt"

	"github.com/tarantool/tt/cli/manifest"
)

// Default platform constraints for a new package. The manifest pipeline itself
// is a tt 3.x feature and the manifest format targets Tarantool 3.x, so these
// are the floors below which nothing here works at all — not a guess at what
// the project needs, which only its author knows.
const (
	defaultTarantoolConstraint = ">=3.0.0"
	defaultTtConstraint        = ">=3.0.0"
)

// skeleton is the manifest tt new writes. It carries two placeholders: the
// format version this build writes, and the package name.
//
// Quoting is double quotes throughout, which is what the manifest editor writes
// when tt package add splices a dependency in. A skeleton quoted differently
// would make the first add look like a reformat in the diff.
//
// Components and products are commented rather than declared. A package needs
// both before tt package build has anything to build, but their contents depend
// on a layout only the author knows, and a wrong products.default is worse than
// an absent one: it builds, quietly, from the wrong files. The commented block
// is there so the next step is visible without a trip to the documentation.
const skeleton = `manifest_version = "%s"

[package]
name = "%s"

# The Tarantool and tt versions this package needs to run.
[platform]
tarantool = "%s"
tt = "%s"

# Runtime dependencies. tt package add <name> [<constraint>] writes here;
# tt package deps shows what they resolved to.
[dependencies]

# Dependencies only the tests and tooling need:
#
# [dev_dependencies]
# luatest = "*"

# A package is built per product, and a product is built from components.
# Declare at least one of each to make tt package build do something:
#
# [components.app]
# path = "."
#
# [products.default]
# components = ["app"]
# default = true
`

// render fills the skeleton in for one package name.
func render(name string) string {
	return fmt.Sprintf(skeleton,
		manifest.ManifestVersion, name,
		defaultTarantoolConstraint, defaultTtConstraint)
}
