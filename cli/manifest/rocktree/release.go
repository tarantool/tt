package rocktree

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/rockspec"
)

// errRevision reports a rock revision below 1.
var errRevision = errors.New("the rock revision is a number from 1")

// releaseHeader begins the rockspec of every rock tt packs (ReleaseRockspec).
const releaseHeader = "-- The rock of the tt package "

// IsReleaseRockspec reports whether src, the text of a rockspec, is that of a
// rock tt packed (ReleaseRockspec), whose build is done: LuaRocks building
// its source rock builds nothing, since only tt builds the package in it.
func IsReleaseRockspec(src []byte) bool {
	return bytes.HasPrefix(src, []byte(releaseHeader))
}

// RockVersionAt is RockVersion with the revision given: the LuaRocks
// version of the rock a package is released as, whose revision is raised to
// publish a new build of a version that is already out.
func RockVersionAt(semver string, revision int) (string, error) {
	if revision < 1 {
		return "", fmt.Errorf("%w: %d", errRevision, revision)
	}

	version, err := RockVersion(semver)
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(version, rockRevision) + "-" + strconv.Itoa(revision), nil
}

// Release describes the rock a tt package is released as: tt package pack
// --format rock. It is the rock a package is installed as, but for other
// projects: its dependencies are the ranges the manifest declares rather
// than the versions the lock of the package pins, and it says where it comes
// from.
type Release struct {
	// Name is the package name, which is the rock's.
	Name string
	// Version is the package's SemVer version; RockVersionAt gives the
	// rock's.
	Version string
	// Revision is the LuaRocks revision of the rock, from 1.
	Revision int
	// Summary, Homepage and License describe the package.
	Summary  string
	Homepage string
	License  string
	// Maintainer names the authors of the package.
	Maintainer string
	// SourceURL is where the source of the package is, a git+ URL; empty is
	// the package's directory, as for the rock it is installed as.
	SourceURL string
	// SourceTag is the git tag of the version, if it is the version of one.
	SourceTag string
	// Platforms are the LuaRocks platforms the package supports (linux,
	// macosx); none is every platform.
	Platforms []string
	// Dependencies are the rocks the package requires, as rock name →
	// constraint expression in LuaRocks syntax; an empty expression is any
	// version.
	Dependencies map[string]string
}

// ReleaseRockspec renders the rockspec of the rock rel and evaluates it, so
// that its RawSource is the rockspec the rock is packed with and published
// beside it. The rock carries the files tt laid out, so its build is done:
// the build type is none, whichever backends built them, and the rockspec is
// the same for the rock of every platform.
func ReleaseRockspec(rel Release) (*luarocks.Rockspec, error) {
	version, err := RockVersionAt(rel.Version, rel.Revision)
	if err != nil {
		return nil, fmt.Errorf("the rock of %s %s: %w", rel.Name, rel.Version, err)
	}

	var src strings.Builder

	fmt.Fprintf(&src, releaseHeader+"%s %s, which tt packs from the\n"+
		"-- files it lays out: its build is done.\n", rel.Name, rel.Version)
	src.WriteString("rockspec_format = \"3.0\"\n")
	fmt.Fprintf(&src, "package = %s\n", luaQuote(rel.Name))
	fmt.Fprintf(&src, "version = %s\n", luaQuote(version))

	src.WriteString("source = {\n")

	if rel.SourceURL == "" {
		src.WriteString("   url = \"file://.\",\n")
	} else {
		fmt.Fprintf(&src, "   url = %s,\n", luaQuote(rel.SourceURL))

		if rel.SourceTag != "" {
			fmt.Fprintf(&src, "   tag = %s,\n", luaQuote(rel.SourceTag))
		}
	}

	src.WriteString("}\n")

	src.WriteString("description = {\n")
	fmt.Fprintf(&src, "   summary = %s,\n", luaQuote(rel.Summary))
	writeField(&src, "homepage", rel.Homepage)
	writeField(&src, "license", rel.License)
	writeField(&src, "maintainer", rel.Maintainer)
	src.WriteString("}\n")

	if len(rel.Platforms) > 0 {
		quoted := make([]string, 0, len(rel.Platforms))
		for _, platform := range rel.Platforms {
			quoted = append(quoted, luaQuote(platform))
		}

		fmt.Fprintf(&src, "supported_platforms = { %s }\n", strings.Join(quoted, ", "))
	}

	writeDependencies(&src, rel.Dependencies)

	src.WriteString("build = {\n   type = \"none\",\n}\n")
	src.WriteString("deploy = {\n   wrap_bin_scripts = false,\n}\n")

	name := rel.Name + "-" + version + ".rockspec"

	spec, err := rockspec.EvalSource(name, []byte(src.String()), luarocks.RockspecConfig{})
	if err != nil {
		return nil, fmt.Errorf("the rockspec of %s %s: %w", rel.Name, rel.Version, err)
	}

	return spec, nil
}

// writeField writes one field of a table, unless its value is empty.
func writeField(src *strings.Builder, key, value string) {
	if value != "" {
		fmt.Fprintf(src, "   %s = %s,\n", key, luaQuote(value))
	}
}

// writeDependencies writes the dependencies table, sorted by rock name, unless
// there are none.
func writeDependencies(src *strings.Builder, dependencies map[string]string) {
	if len(dependencies) == 0 {
		return
	}

	names := make([]string, 0, len(dependencies))
	for name := range dependencies {
		names = append(names, name)
	}

	slices.Sort(names)

	src.WriteString("dependencies = {\n")

	for _, name := range names {
		dependency := name
		if expr := dependencies[name]; expr != "" {
			dependency += " " + expr
		}

		fmt.Fprintf(src, "   %s,\n", luaQuote(dependency))
	}

	src.WriteString("}\n")
}
