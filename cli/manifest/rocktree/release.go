package rocktree

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
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
// rock tt packed (ReleaseRockspec), whose source rock holds a tt package: tt
// builds it from its manifest, and LuaRocks by the build table of the
// rockspec. The build table of an older pack built nothing, so LuaRocks
// installed its source rock with no module in it.
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
	// SourceCommit is the full sha of the commit of the version, when the
	// version is of a commit and of no tag: source.tag holds it, as LuaRocks
	// has no field for a commit. Ignored with a SourceTag.
	SourceCommit string
	// Uncommitted reports a version of uncommitted changes, which no commit
	// holds the source of: the source names the repository alone.
	Uncommitted bool
	// Platforms are the LuaRocks platforms the package supports (linux,
	// macosx); none is every platform.
	Platforms []string
	// Dependencies are the rocks the package requires, as rock name →
	// constraint expression in LuaRocks syntax; an empty expression is any
	// version.
	Dependencies map[string]string
	// Build is how LuaRocks builds the package from its source rock, or from
	// the source the rockspec names, without tt.
	Build ReleaseBuild
}

// ReleaseRockspec renders the rockspec of the rock rel and evaluates it, so
// that its RawSource is the rockspec the rock is packed with and published
// beside it: one for the binary rock of every platform, the source rock and
// the rockspec alone. LuaRocks installs a binary rock as it is, whatever its
// build table says; it builds the source rock and the bare rockspec as
// rel.Build says (ReleaseBuild), and tt builds them from the manifest in the
// sources, reading no build table at all. A path of rel.Build.Install
// build.install cannot say fails it (CheckInstallDest).
func ReleaseRockspec(rel Release) (*luarocks.Rockspec, error) {
	version, err := RockVersionAt(rel.Version, rel.Revision)
	if err != nil {
		return nil, fmt.Errorf("the rock of %s %s: %w", rel.Name, rel.Version, err)
	}

	var src strings.Builder

	luaRocks := "LuaRocks builds it as the build table says"
	if rel.Build.TtOnly != "" {
		luaRocks = "LuaRocks cannot, as the build table says"
	}

	fmt.Fprintf(&src, releaseHeader+"%s %s.\n"+
		"-- tt package pack generated it from app.manifest.toml. tt builds the\n"+
		"-- package from that manifest; %s.\n", rel.Name, rel.Version, luaRocks)
	src.WriteString("rockspec_format = \"3.0\"\n")
	fmt.Fprintf(&src, "package = %s\n", luaQuote(rel.Name))
	fmt.Fprintf(&src, "version = %s\n", luaQuote(version))

	writeSource(&src, rel)

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

	err = writeBuild(&src, rel.Build)
	if err != nil {
		return nil, fmt.Errorf("the rockspec of %s %s: %w", rel.Name, rel.Version, err)
	}

	src.WriteString("deploy = {\n   wrap_bin_scripts = false,\n}\n")

	name := rel.Name + "-" + version + ".rockspec"

	spec, err := rockspec.EvalSource(name, []byte(src.String()), luarocks.RockspecConfig{})
	if err != nil {
		return nil, fmt.Errorf("the rockspec of %s %s: %w", rel.Name, rel.Version, err)
	}

	return spec, nil
}

// writeSource writes the source table of the rockspec of rel: the package's
// directory without a SourceURL, and otherwise the repository at the commit
// of the version. A tag names it as the tag; a commit with no tag is named by
// its full sha in source.tag, there being no field of a commit, which LuaRocks
// cannot fetch - git clone --branch takes no commit - and so fails on rather
// than fetch another commit. A version of uncommitted changes names the
// repository with no commit, and a comment says why; one a VERSION file gives
// names it with no commit too, the version not being derived from one.
func writeSource(src *strings.Builder, rel Release) {
	src.WriteString("source = {\n")

	switch {
	case rel.SourceURL == "":
		src.WriteString("   url = \"file://.\",\n")
	case rel.SourceTag != "":
		fmt.Fprintf(src, "   url = %s,\n", luaQuote(rel.SourceURL))
		fmt.Fprintf(src, "   tag = %s,\n", luaQuote(rel.SourceTag))
	case rel.Uncommitted:
		src.WriteString("   -- The version is of uncommitted changes, which no commit holds.\n")
		fmt.Fprintf(src, "   url = %s,\n", luaQuote(rel.SourceURL))
	case rel.SourceCommit != "":
		fmt.Fprintf(src, "   url = %s,\n", luaQuote(rel.SourceURL))
		src.WriteString("   -- The commit of the version, which no tag names. LuaRocks fetches\n" +
			"   -- a tag with git clone --branch, which takes no commit.\n")
		fmt.Fprintf(src, "   tag = %s,\n", luaQuote(rel.SourceCommit))
	default:
		fmt.Fprintf(src, "   url = %s,\n", luaQuote(rel.SourceURL))
	}

	src.WriteString("}\n")
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

	src.WriteString("dependencies = {\n")

	for _, name := range slices.Sorted(maps.Keys(dependencies)) {
		dependency := name
		if expr := dependencies[name]; expr != "" {
			dependency += " " + expr
		}

		fmt.Fprintf(src, "   %s,\n", luaQuote(dependency))
	}

	src.WriteString("}\n")
}
