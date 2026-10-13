package install

import (
	"errors"
	"fmt"
	"slices"

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

// masterInstallVersion describes the explicitly supported master revision.
func masterInstallVersion() resolvedInstallVersion {
	return resolvedInstallVersion{
		kind: versionKindMaster,
		ref:  masterVersion,
		name: masterVersion,
	}
}

// resolveLatestVersion selects the product's default tag when no version was requested.
func resolveLatestVersion(installCtx InstallCtx) (resolvedInstallVersion, error) {
	log.Infof("Getting latest %s version...", installCtx.Program.String())

	versions, err := getVersionsFromRepo(installCtx)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	tag, err := defaultInstallTag(installCtx.Program, versions)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	return resolvedInstallVersion{kind: versionKindTag, ref: tag, name: tag}, nil
}

// defaultInstallTag selects the latest stable CE release or the latest tt tag.
func defaultInstallTag(program binary.Program, versions []version.Version) (string, error) {
	switch program {
	case binary.ProgramCe:
		latest := getLatestRelease(versions)
		if latest == "" {
			return "", errNoVersionFound
		}

		return latest, nil
	case binary.ProgramTt:
		if len(versions) == 0 {
			return "", errNoVersionsWereFetched
		}

		return versions[len(versions)-1].Str, nil
	default:
		return "", fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

// resolveTagVersion matches only release tags; the caller handles a numeric SHA fallback.
func resolveTagVersion(installCtx InstallCtx) (resolvedInstallVersion, error) {
	log.Infof("Searching in versions...")

	versions, err := getVersionsFromRepo(installCtx)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	tag, err := version.MatchVersion(installCtx.requestedVersion, versions)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	return resolvedInstallVersion{kind: versionKindTag, ref: tag, name: tag}, nil
}

// resolvePullRequestVersion verifies a pull request and assigns its installation name.
func resolvePullRequestVersion(installCtx InstallCtx, pullRequestID string) (
	resolvedInstallVersion,
	error,
) {
	log.Infof("Searching in pull-requests...")

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
