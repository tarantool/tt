package binary

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
)

// minSupportedMajorVersion is the first major version of Tarantool tt installs.
const minSupportedMajorVersion = 3

// debugFlavor marks a debug build among the flavours of an artifact.
const debugFlavor = "debug"

// ErrNoBundleSource reports a product no source of this build of tt has.
var ErrNoBundleSource = errors.New("no source of archives")

// bundleSources are the sources of the process; nil until SetBundleSources.
var bundleSources atomic.Pointer[[]bundle.Source]

// SetBundleSources makes sources the sources of prebuilt archives of the
// process, in the order they are asked in. The core sets them once, when tt
// boots and before any command is built.
func SetBundleSources(sources []bundle.Source) {
	sources = slices.Clone(sources)
	bundleSources.Store(&sources)
}

// BundleSources returns the sources of the process.
func BundleSources() []bundle.Source {
	sources := bundleSources.Load()
	if sources == nil {
		return nil
	}

	return *sources
}

// BundleSource returns the first source of the process with archives of
// program. Without one the error wraps ErrNoBundleSource.
func BundleSource(program Program) (bundle.Source, error) {
	for _, source := range BundleSources() {
		if slices.Contains(source.Products(), program.String()) {
			return source, nil
		}
	}

	return nil, fmt.Errorf("%w of %s in this build of tt; "+
		"use --local-dir with a directory of downloaded archives", ErrNoBundleSource, program)
}

// IsMasked reports whether a version of program is no longer offered for
// installation: Tarantool 1.x and 2.x, Community or Enterprise, are not
// supported. SDK archives carry the version of their Tarantool; tt, tt-ee and
// TCM have versions of their own.
func IsMasked(program Program, ver version.Version) bool {
	return isTarantoolRelease(program) && ver.Major < minSupportedMajorVersion
}

// isTarantoolRelease reports whether the versions of program are those of
// Tarantool: Community or Enterprise, or the SDK.
func isTarantoolRelease(program Program) bool {
	return program == ProgramCe || program.BundleSource() == ProgramSDK
}

// ErrUnsupportedVersion reports a version of Tarantool tt no longer installs.
var ErrUnsupportedVersion = errors.New("unsupported version")

// requestedVersionPattern matches a version a user asks for: X, X.Y, X.Y.Z,
// with a pre-release or a build after a dash, and an archive name with a
// build name before the version, gc64-3.8.0-0-r94. Its submatch is the
// major version.
var requestedVersionPattern = regexp.MustCompile(
	`^(?:[A-Za-z][A-Za-z0-9]*-)?v?(\d+)(?:\.\d+){0,2}(?:-[0-9A-Za-z.-]+)?$`)

// CheckRequestedVersion reports a version of program tt no longer installs
// (IsMasked): a Tarantool, Community or Enterprise, or an SDK, older than
// 3.0. The error wraps ErrUnsupportedVersion and names the version
// requested; anything else, a commit or a newer version, is nil. A request
// that may be a commit, seven digits or more without a dot, is not taken for
// a version.
func CheckRequestedVersion(program Program, requested string) error {
	selected := strings.TrimPrefix(requested, SDKVersionPrefix)

	match := requestedVersionPattern.FindStringSubmatch(selected)
	if match == nil || !strings.Contains(selected, ".") && len(selected) >= MinCommitHashLength {
		return nil
	}

	major, err := strconv.ParseUint(match[1], 10, 64)
	if err == nil && isTarantoolRelease(program) && major < minSupportedMajorVersion {
		return fmt.Errorf("%w: %s %s: tt %d installs Tarantool %d.0 and later only; "+
			"use tt 2 to install Tarantool %s", ErrUnsupportedVersion, program, requested,
			version.Major, minSupportedMajorVersion, requested)
	}

	return nil
}

// Bundle is a build of a product a source offers.
type Bundle struct {
	// Source is where the archive comes from.
	Source bundle.Source
	// Artifact is the archive, as the source listed it.
	Artifact bundle.Artifact
	// Version identifies the build: Str is the flavours of the artifact
	// followed by its version, Tarball the name of the archive.
	Version version.Version
}

// IsDebug reports whether the build is a debug one.
func (b Bundle) IsDebug() bool {
	return slices.Contains(b.Artifact.Flavor, debugFlavor)
}

// Bundles are builds sorted from the oldest to the newest.
type Bundles []Bundle

// NewBundleQuery asks for the builds of program for the host: development
// builds if dev, those of the release line version if it is not empty.
func NewBundleQuery(program Program, dev bool, version string) bundle.Query {
	channel := bundle.ChannelRelease
	if dev {
		channel = bundle.ChannelDev
	}

	return bundle.Query{
		Product: program.String(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Channel: channel,
		Version: version,
	}
}

// ArtifactVersion is the version tt identifies the build of an artifact by:
// its flavours, then its version, joined by dashes, as the archive names them.
func ArtifactVersion(artifact bundle.Artifact) string {
	return strings.Join(append(slices.Clone(artifact.Flavor), artifact.Version), "-")
}

// ListBundles asks source for the builds of program query names. It leaves
// out the builds tt no longer installs and those with a version tt cannot read.
func ListBundles(ctx context.Context, source bundle.Source, program Program,
	query bundle.Query,
) (Bundles, error) {
	artifacts, err := source.List(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list %s archives in %s: %w", program, source.Name(), err)
	}

	bundles := make(Bundles, 0, len(artifacts))

	for _, artifact := range artifacts {
		parsed, err := version.Parse(ArtifactVersion(artifact))
		if err != nil {
			log.Debugf("failed to parse the version of %s archive %s from %s: %v",
				program, artifact.Name, source.Name(), err)

			continue
		}

		if IsMasked(program, parsed) {
			continue
		}

		parsed.Tarball = artifact.Name

		bundles = append(bundles, Bundle{Source: source, Artifact: artifact, Version: parsed})
	}

	sort.SliceStable(bundles, func(i, j int) bool {
		return LessVersion(bundles[i].Version, bundles[j].Version)
	})

	return bundles, nil
}

// ReleaseSource is a source that lists the tags of the releases of its
// product, as the releases of tt on GitHub do: its List answers for a
// release tag only.
type ReleaseSource interface {
	Releases(ctx context.Context) ([]string, error)
}

// PlatformSource is a source that knows the platforms its releases publish
// archives for, as the releases of tt on GitHub do. A host it publishes none
// for is an error that names the platforms there are, not an archive that
// is not found.
type PlatformSource interface {
	CheckPlatform(tag, goos, goarch string) error
}

// PrebuiltVersions returns the versions of the releases of program source
// has prebuilt builds of for the host, sorted as the Git tags are: the tags
// of the releases of a ReleaseSource, those a PlatformSource publishes
// archives for the host of, or the versions of the builds without flavours
// the source lists for the host. The versions tt no longer installs, and
// those it cannot read, are left out. tt binary install resolves a version
// among them, and tt binary search lists them, without git.
func PrebuiltVersions(ctx context.Context, source bundle.Source, program Program) (
	version.VersionSlice, error,
) {
	if releases, ok := source.(ReleaseSource); ok {
		tags, err := releases.Releases(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.Name(), err)
		}

		platforms, _ := source.(PlatformSource)

		return releaseVersions(program, tags, platforms), nil
	}

	bundles, err := ListBundles(ctx, source, program, NewBundleQuery(program, false, ""))
	if err != nil {
		return nil, err
	}

	var versions version.VersionSlice

	for _, found := range bundles {
		if len(found.Artifact.Flavor) == 0 {
			versions = append(versions, found.Version)
		}
	}

	sort.Stable(versions)

	return versions, nil
}

// releaseVersions returns the versions of the release tags of program,
// sorted, but those tt no longer installs, those it cannot read and, with
// platforms, those that publish no archive for the host.
func releaseVersions(program Program, tags []string, platforms PlatformSource,
) version.VersionSlice {
	versions := make(version.VersionSlice, 0, len(tags))

	for _, tag := range tags {
		parsed, err := version.Parse(tag)
		if err != nil || IsMasked(program, parsed) ||
			platforms != nil && platforms.CheckPlatform(tag, runtime.GOOS, runtime.GOARCH) != nil {
			continue
		}

		versions = append(versions, parsed)
	}

	sort.Stable(versions)

	return versions
}

// LessVersion orders the versions of builds: by number, release type,
// commits past the tag and revision.
func LessVersion(verLeft, verRight version.Version) bool {
	left := []uint64{
		verLeft.Major, verLeft.Minor,
		verLeft.Patch, uint64(verLeft.Release.Type),
		verLeft.Release.Num, verLeft.Additional, verLeft.Revision,
	}
	right := []uint64{
		verRight.Major, verRight.Minor,
		verRight.Patch, uint64(verRight.Release.Type),
		verRight.Release.Num, verRight.Additional, verRight.Revision,
	}

	return slices.Compare(left, right) < 0
}
