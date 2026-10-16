package binary

import (
	"regexp"
	"strings"

	"github.com/tarantool/tt/v3/cli/version"
)

var (
	productEditionSuffix     = regexp.MustCompile(`^(.+?)(ce|ee)$`)
	tarantoolEnterpriseBuild = regexp.MustCompile(`^(gc64|nogc64|debug)-`)
)

// VersionRequest identifies a product and an unresolved version selector.
// The selector can be a release mask, a source revision, or an archive name.
type VersionRequest struct {
	Program Program
	Version string
}

// ParseVersionRequest normalizes CLI edition selectors before dispatching an operation.
// Bare tt/Tarantool versions, commits, and pull requests select CE; release
// selectors ending in ce/ee select that edition. SDK component names select EE,
// as do Tarantool Enterprise build names. Explicit product names stay explicit.
func ParseVersionRequest(programName, requested string) (VersionRequest, error) {
	program, err := ParseProgram(programName)
	if err != nil {
		return VersionRequest{}, err
	}

	request := VersionRequest{Program: program, Version: requested}
	enterprise := program.EnterpriseProgram()

	if enterprise == ProgramUnknown {
		return request, nil
	}

	// A commit ending in ce or ee must keep its complete identity.
	if isHash, _ := IsValidCommitHash(requested); isHash {
		return request, nil
	}

	match := productEditionSuffix.FindStringSubmatch(requested)
	switch {
	case match != nil && version.IsVersion(match[1]):
		request.Version = match[1]
		if match[2] == "ee" {
			request.Program = enterprise
		}
	case program == ProgramCe && tarantoolEnterpriseBuild.MatchString(requested),
		strings.HasPrefix(requested, SDKVersionPrefix):
		request.Program = enterprise
	}

	return request, nil
}

// VersionArgument makes an installed identity usable with its unified product
// command. Numeric EE identities need an explicit edition suffix.
func VersionArgument(program Program, installed string) string {
	community := program.FallbackProgram()
	if community == ProgramUnknown {
		return installed
	}

	request, _ := ParseVersionRequest(community.String(), installed)
	if request.Program != program {
		return installed + "ee"
	}

	return installed
}
