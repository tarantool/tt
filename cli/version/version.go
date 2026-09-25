package version

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	goVersion "github.com/hashicorp/go-version"

	"github.com/tarantool/tt/sdk"
)

const (
	unknownVersion = "<unknown>"

	// DefaultTitle is the name tt presents itself under.
	DefaultTitle = "Tarantool CLI"
)

// Get the value of this variables at build time.
// See magefile for more details.
var (
	gitTag            string
	gitCommit         string
	gitCommitSinceTag string
	versionLabel      string
)

// Info describes what a build was made from.
type Info struct {
	// Tag is the tag the build is at or descends from, as git describe
	// prints it: v2.15.0, or v2.15.0-3-gabc1234 for a commit past it. Only
	// its numeric part makes the version. Empty means the version is
	// unknown.
	Tag string
	// Commit is the abbreviated hash of the commit built.
	Commit string
	// CommitsSinceTag is the number of commits between Tag and Commit. When
	// it is not zero the full version shows Tag as well.
	CommitsSinceTag int
	// Label, when set, follows the version: 2.15.0/label.
	Label string
}

// Flavour is how a distribution of tt presents itself: its name and its
// version. The zero Flavour is tt's own.
type Flavour struct {
	// Title is the distribution's name. Empty means DefaultTitle.
	Title string
	// Version is the distribution's version. Nil means the core's.
	Version *Info
	// Edition, when set, follows the short version as semver build
	// metadata: 2.15.0+ee.
	Edition string
}

// Name returns the name the distribution presents itself under.
func (f Flavour) Name() string {
	if f.Title == "" {
		return DefaultTitle
	}

	return f.Title
}

// Custom reports whether the distribution presents itself under a name
// other than tt's own.
func (f Flavour) Custom() bool {
	return f.Name() != DefaultTitle
}

// GetVersion returns the distribution's version: in full with its name, the
// platform and the commit; short, the version alone; or, with needCommit,
// the version followed by the commit. The edition follows the version in
// the short forms.
func (f Flavour) GetVersion(showShort, needCommit bool) string {
	release := coreRelease()
	if f.Version != nil {
		release = f.Version.release()
	}

	return release.format(f.Name(), f.Edition, showShort, needCommit)
}

// GetVersion returns the version of the tt core, in the forms
// Flavour.GetVersion describes. It is the version the core is, whichever
// distribution of tt runs it.
func GetVersion(showShort, needCommit bool) string {
	return coreRelease().format(DefaultTitle, "", showShort, needCommit)
}

// release is a version as it is printed.
type release struct {
	// version is the numeric version with its label, or unknownVersion.
	version string
	// commit is the abbreviated hash of the commit built.
	commit string
	// exact, when set, is the precise version the full form shows next to
	// the commit: a build past the tag version names.
	exact string
}

// readCoreVersion returns the version of the tt core Go recorded in the
// build info, and false when it recorded none.
var readCoreVersion = sdk.CoreVersion

// coreRelease returns the version of the tt core: from the values set at
// build time, or, for a build that set no tag - a program with tt as a
// dependency, say - from the module version Go recorded.
func coreRelease() release {
	if gitTag == "" {
		if module, ok := readCoreVersion(); ok {
			return moduleRelease(module)
		}
	}

	return tagRelease(gitTag, gitCommit, versionLabel,
		gitCommitSinceTag != "" && gitCommitSinceTag != "0")
}

// pseudoVersion matches a Go pseudo-version, the module version of an
// untagged commit, without build metadata. Its submatches are the major,
// minor and patch numbers, the pre-release of the tag it descends from, "0"
// when it descends from a tag at all, and the abbreviated commit hash.
var pseudoVersion = regexp.MustCompile(
	`^v(\d+)\.(\d+)\.(\d+)-(?:(?:([0-9A-Za-z.-]+)\.)?(0)\.)?\d{14}-([0-9a-f]{12})$`,
)

// moduleRelease returns the version of a core Go recorded as the module
// version module. As for a build from a git checkout, the version is the
// tag's the build is at or descends from, and a build other than the tag
// itself shows the module version in full next to the commit:
//
//	module version                            reported   commit        in full
//	v3.1.0                                    3.1.0      <unknown>     no
//	v3.1.0+dirty                              3.1.0      <unknown>     yes
//	v3.1.1-0.20260925120000-abcdef123456      3.1.0      abcdef123456  yes
//	v3.1.0-rc1.0.20260925120000-abcdef123456  3.1.0      abcdef123456  yes
//	v3.0.0-20260925120000-abcdef123456        <unknown>  abcdef123456  yes
//
// The last is a commit no tag precedes. A tag records no commit; the commit
// of a pseudo-version is the abbreviated hash it carries.
func moduleRelease(module string) release {
	plain, metadata, _ := strings.Cut(module, "+")

	match := pseudoVersion.FindStringSubmatch(plain)
	if match == nil {
		exact := ""
		if metadata != "" {
			exact = module
		}

		return release{version: numericVersion(plain), commit: unknownVersion, exact: exact}
	}

	version := unknownVersion
	if tag := pseudoVersionTag(match); tag != "" {
		version = numericVersion(tag)
	}

	return release{version: version, commit: match[6], exact: module}
}

// pseudoVersionTag returns the tag the pseudo-version pseudoVersion matched
// as match descends from, or "" when it descends from none.
func pseudoVersionTag(match []string) string {
	major, minor, patch, pre, descends := match[1], match[2], match[3], match[4], match[5]

	switch {
	case descends == "":
		return ""
	case pre != "":
		return fmt.Sprintf("v%s.%s.%s-%s", major, minor, patch, pre)
	}

	// The patch number of a pseudo-version past a release is one more than
	// the release's.
	number, err := strconv.Atoi(patch)
	if err != nil || number == 0 {
		return ""
	}

	return fmt.Sprintf("v%s.%s.%d", major, minor, number-1)
}

// release returns the version info describes.
func (info Info) release() release {
	return tagRelease(info.Tag, info.Commit, info.Label, info.CommitsSinceTag != 0)
}

// tagRelease returns the version of a build of commit, at or past (when
// pastTag is set) the tag tag, labelled label.
func tagRelease(tag, commit, label string, pastTag bool) release {
	if tag == "" {
		return release{version: unknownVersion, commit: commit, exact: ""}
	}

	version := numericVersion(tag)
	if label != "" {
		version = fmt.Sprintf("%s/%s", version, label)
	}

	exact := ""
	if pastTag {
		exact = tag
	}

	return release{version: version, commit: commit, exact: exact}
}

// numericVersion returns the numeric part of tag - 2.15.0 for v2.15.0-3-g1 -
// or tag itself when it is not a version.
func numericVersion(tag string) string {
	parsed, err := goVersion.NewVersion(tag)
	if err != nil {
		return tag
	}

	segments := parsed.Segments()
	numbers := make([]string, 0, len(segments))

	for _, num := range segments {
		numbers = append(numbers, strconv.Itoa(num))
	}

	return strings.Join(numbers, ".")
}

// format renders r for the distribution title with edition, in the form
// showShort and needCommit select.
func (r release) format(title, edition string, showShort, needCommit bool) string {
	short := r.version
	if edition != "" {
		short += "+" + edition
	}

	switch {
	case needCommit:
		return fmt.Sprintf("%s.%s", short, r.commit)
	case showShort:
		return short
	case r.exact != "":
		return fmt.Sprintf("%s version %s, %s/%s. commit: %s (%s)",
			title, r.version, runtime.GOOS, runtime.GOARCH, r.commit, r.exact)
	default:
		return fmt.Sprintf("%s version %s, %s/%s. commit: %s",
			title, r.version, runtime.GOOS, runtime.GOARCH, r.commit)
	}
}
