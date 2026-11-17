package install

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errNoVersionFound        = errors.New("no version found")
	errNoVersionsWereFetched = errors.New("no versions were fetched")
	errUnknownVersionKind    = errors.New("unknown installation version kind")
)

const (
	// masterVersion is the version that stands for the master branch.
	masterVersion = "master"
)

// versionKind identifies the supported ways to select source code for installation.
type versionKind int

const (
	versionKindUnknown versionKind = iota
	versionKindTag
	versionKindCommit
	// master is supported explicitly; arbitrary branches are not installation versions.
	versionKindMaster
	versionKindPullRequest
)

// resolvedInstallVersion contains a resolved source revision, independent of its product.
type resolvedInstallVersion struct {
	kind versionKind
	// ref is the Git checkout target: a tag, full SHA, master or a fetched pr/<id> branch.
	ref string
	// name identifies the installation: a tag, master or a shortened resolved revision.
	name string
}

// installName returns the product-prefixed name used for installation files and directories.
func (resolved resolvedInstallVersion) installName(program binary.Program) string {
	return program.String() + version.FsSeparator + resolved.name
}

// resolveInstallVersion resolves a tag, commit, master or pull request for tt or Tarantool CE.
func resolveInstallVersion(installCtx InstallCtx) (resolvedInstallVersion, error) {
	requested := installCtx.requestedVersion

	// Tarantool 1.10 and 2.x are not offered, and the request is not taken
	// for a commit either.
	err := binary.CheckRequestedVersion(installCtx.Program, requested)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	isPullRequest, pullRequestID := binary.IsPullRequest(requested)

	switch {
	case requested == "":
		return resolveLatestVersion(installCtx)
	case requested == masterVersion:
		return masterInstallVersion(), nil
	case isPullRequest:
		return resolvePullRequestVersion(installCtx, pullRequestID)
	case version.IsVersion(requested):
		resolved, err := resolveTagVersion(installCtx)
		switch {
		case err == nil:
			return resolved, nil
		case errors.Is(err, version.ErrNotFound):
			// An all-numeric SHA can also look like a version mask.
			return resolveCommitVersion(installCtx)
		default:
			return resolvedInstallVersion{}, err
		}
	default:
		return resolveCommitVersion(installCtx)
	}
}

// tagInstallVersion describes a release tag. The installation is named by
// the version without the v of a tt tag, as Tarantool is: tt_3.0.0, the
// version tt binary list shows and tt binary switch takes.
func tagInstallVersion(tag string) resolvedInstallVersion {
	return resolvedInstallVersion{
		kind: versionKindTag,
		ref:  tag,
		name: strings.TrimPrefix(tag, "v"),
	}
}

// masterInstallVersion describes the explicitly supported master revision.
func masterInstallVersion() resolvedInstallVersion {
	return resolvedInstallVersion{
		kind: versionKindMaster,
		ref:  masterVersion,
		name: masterVersion,
	}
}

// takesPrebuilt reports whether the install of a release takes the prebuilt
// build the source of the program has: --source-build, --local-repo and, of
// tarantool, --dynamic and --use-docker build it from source instead.
func takesPrebuilt(installCtx InstallCtx) bool {
	switch installCtx.Program {
	case binary.ProgramCe:
		return !installCtx.SourceBuild && !installCtx.Dynamic && !installCtx.BuildInDocker &&
			installCtx.LocalRepo == ""
	case binary.ProgramTt:
		return !installCtx.SourceBuild && installCtx.LocalRepo == ""
	default:
		return false
	}
}

// prebuiltMiss tells why the source of the prebuilt builds of a program
// resolved no release.
type prebuiltMiss struct {
	// source is the name of the source.
	source string
	// err is what the source answered: a failure, or no release that matches.
	err error
	// suggestion is a release the source has, to ask for instead, if any.
	suggestion string
}

// resolvePrebuiltTag resolves the requested version among the releases the
// source of the prebuilt builds of the program has (binary.PrebuiltVersions), as
// version.MatchVersion resolves it among the Git tags, so that the install
// of a prebuilt release needs no git. No version requested is the latest
// release: the one a latestReleaseSource names, or the default tag
// (defaultInstallTag) of the releases. A pushed tag may still have no
// release, and a release no prebuilt build yet, so the newest tag is not a
// reliable choice for prebuilt delivery.
//
// It returns no tag and no miss when the install takes no prebuilt build
// (takesPrebuilt) or this build of tt has no source of the program: the
// Git tags resolve the version then, as they do after a miss. A miss is a
// source that fails, or that has no release the request matches: a
// development tag such as 3.5.0-entrypoint, a release with no prebuilt
// build for the host yet, a commit.
func resolvePrebuiltTag(ctx context.Context, installCtx InstallCtx) (string, *prebuiltMiss) {
	if !takesPrebuilt(installCtx) {
		return "", nil
	}

	program, requested := installCtx.Program, installCtx.requestedVersion

	source, err := binary.BundleSource(program)
	if err != nil {
		return "", nil
	}

	missed := func(err error, suggestion string) *prebuiltMiss {
		return &prebuiltMiss{source: source.Name(), err: err, suggestion: suggestion}
	}

	if latest, ok := source.(latestReleaseSource); ok && requested == "" {
		tag, err := latest.Latest(ctx)
		if err != nil {
			return "", missed(fmt.Errorf("%w of %s in %s: %w", errNoLatestRelease, program,
				source.Name(), err), "")
		}

		return tag, nil
	}

	versions, err := binary.PrebuiltVersions(ctx, source, program)
	if err != nil {
		return "", missed(err, "")
	}

	if len(versions) == 0 {
		return "", missed(fmt.Errorf("%w: %s has no prebuilt %s for %s/%s",
			errPrebuiltPackageUnavailable, source.Name(), program, runtime.GOOS,
			runtime.GOARCH), "")
	}

	latest, latestErr := defaultInstallTag(program, versions)
	if requested == "" {
		if latestErr != nil {
			return "", missed(fmt.Errorf("%w: %s has no prebuilt release of %s: %w",
				errPrebuiltPackageUnavailable, source.Name(), program, latestErr), "")
		}

		return latest, nil
	}

	tag, err := version.MatchVersion(requested, versions)
	if err != nil {
		suggestion := ""
		if latestErr == nil {
			suggestion = strings.TrimPrefix(latest, "v")
		}

		return "", missed(fmt.Errorf("%w: %s has no prebuilt %s %s",
			errPrebuiltPackageUnavailable, source.Name(), program, requested), suggestion)
	}

	return tag, nil
}

// resolveLatestVersion selects the product's default tag when no version was requested.
func resolveLatestVersion(installCtx InstallCtx) (resolvedInstallVersion, error) {
	log.Infof("Getting latest %s version...", installCtx.Program.String())

	tag, miss := resolvePrebuiltTag(context.Background(), installCtx)
	if tag != "" {
		return tagInstallVersion(tag), nil
	}

	if miss != nil {
		log.Debugf("Latest prebuilt %s is unavailable, using the newest tag: %v",
			installCtx.Program, miss.err)
	}

	err := requireGit("find the latest "+installCtx.Program.String(), miss)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	versions, err := getVersionsFromRepo(installCtx)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	tag, err = defaultInstallTag(installCtx.Program, versions)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	return tagInstallVersion(tag), nil
}

// defaultInstallTag selects the latest stable CE release or the latest tt
// tag of the major version of this tt: the newest stable one, or the newest
// pre-release while the major version has no stable one, as the latest
// release of tt is chosen.
func defaultInstallTag(program binary.Program, versions []version.Version) (string, error) {
	switch program {
	case binary.ProgramCe:
		latest := getLatestRelease(versions)
		if latest == "" {
			return "", errNoVersionFound
		}

		return latest, nil
	case binary.ProgramTt:
		var ownMajor []version.Version

		for _, candidate := range versions {
			if candidate.Major == version.Major {
				ownMajor = append(ownMajor, candidate)
			}
		}

		if len(ownMajor) == 0 {
			return "", fmt.Errorf("%w: no tag of tt %d", errNoVersionsWereFetched, version.Major)
		}

		latest := getLatestRelease(ownMajor)
		if latest == "" {
			latest = ownMajor[len(ownMajor)-1].Str
		}

		return latest, nil
	default:
		return "", fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

// resolveTagVersion matches only tags: those of the releases the source of
// prebuilt builds has (resolvePrebuiltTag), then the Git tags. The caller
// handles a numeric SHA fallback.
func resolveTagVersion(installCtx InstallCtx) (resolvedInstallVersion, error) {
	log.Infof("Searching in versions...")

	program, requested := installCtx.Program, installCtx.requestedVersion

	tag, miss := resolvePrebuiltTag(context.Background(), installCtx)
	if tag != "" {
		return tagInstallVersion(tag), nil
	}

	if miss != nil {
		log.Debugf("%v; searching in the Git tags", miss.err)
	}

	err := requireGit("find "+program.String()+" "+requested, miss)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	versions, err := getVersionsFromRepo(installCtx)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	tag, err = version.MatchVersion(requested, versions)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	return tagInstallVersion(tag), nil
}

// resolvePullRequestVersion verifies a pull request and assigns its installation name.
func resolvePullRequestVersion(installCtx InstallCtx, pullRequestID string) (
	resolvedInstallVersion,
	error,
) {
	log.Infof("Searching in pull-requests...")

	err := requireGit(fmt.Sprintf("find pull request %s of %s", pullRequestID,
		installCtx.Program), nil)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	commit, err := getPullRequestCommit(installCtx, pullRequestID)
	if err != nil {
		return resolvedInstallVersion{},
			fmt.Errorf("%s%w", installCtx.requestedVersion, errUnableToGetPullRequestInfo)
	}

	return resolvedInstallVersion{
		kind: versionKindPullRequest,
		ref:  "pr/" + pullRequestID,
		name: commit[:binary.MinCommitHashLength],
	}, nil
}

// resolveCommitVersion verifies a commit and assigns its installation name.
func resolveCommitVersion(installCtx InstallCtx) (resolvedInstallVersion, error) {
	requested := installCtx.requestedVersion

	log.Infof("Searching in commits...")

	isValid, err := binary.IsValidCommitHash(requested)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	if !isValid {
		return resolvedInstallVersion{}, errHashHasAWrongFormat
	}

	err = requireGit(fmt.Sprintf("find commit %s of %s", requested, installCtx.Program), nil)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	ref, err := getCommit(installCtx, requested)
	if err != nil {
		return resolvedInstallVersion{}, fmt.Errorf("%s%w", requested, errUnableToGetHashInfo)
	}

	return resolvedInstallVersion{
		kind: versionKindCommit,
		ref:  ref,
		// git show can return a tag name shorter than a commit abbreviation.
		name: ref[:min(len(ref), binary.MinCommitHashLength)],
	}, nil
}

func getLatestRelease(versions []version.Version) string {
	for _, candidate := range slices.Backward(versions) {
		if candidate.Release.Type == version.TypeRelease {
			return candidate.Str
		}
	}

	return ""
}
