package binary

import (
	"errors"
	"fmt"
)

var (
	errUnknownProgram = errors.New("unknown program: ")
)

// Program represents a strictly typed enum for program types.
type Program int

const (
	ProgramUnknown Program = iota
	ProgramCe              // tarantool.
	ProgramEe              // tarantool-ee.
	ProgramTt              // tt.
	ProgramTcm             // tcm.
	ProgramSDK             // tarantool-sdk.
	ProgramTtEe            // tt-ee.
)

// Executable names are shared by Community and Enterprise products.
const (
	tarantoolName = "tarantool"
	ttName        = "tt"
)

// programToExec contains executables matched for each Program types.
var programToExec = map[Program]string{
	ProgramCe:   tarantoolName,
	ProgramEe:   tarantoolName,
	ProgramTt:   ttName,
	ProgramTtEe: ttName,
	ProgramTcm:  "tcm",
}

// programToString contains string representations for each type.
var programToString = map[Program]string{
	ProgramCe:   tarantoolName,
	ProgramEe:   "tarantool-ee",
	ProgramTt:   ttName,
	ProgramTtEe: "tt-ee",
	ProgramTcm:  "tcm",
	ProgramSDK:  "tarantool-sdk",
}

// programToGitRepo contains source repository URLs for programs built from Git.
var programToGitRepo = map[Program]string{
	ProgramCe: "https://github.com/tarantool/tarantool.git",
	ProgramTt: "https://github.com/tarantool/tt.git",
}

// stringToProgram contains the reverse mapping for efficient lookup.
var stringToProgram = func() map[string]Program {
	result := make(map[string]Program, len(programToString))
	for k, v := range programToString {
		result[v] = k
	}

	result["sdk"] = ProgramSDK
	result["SDK"] = ProgramSDK

	return result
}()

// String returns a string representation of Program type.
func (p Program) String() string {
	if s, ok := programToString[p]; ok {
		return s
	}

	return fmt.Sprintf("unknown(%d)", p)
}

// ParseProgram converts the string to Program type.
func ParseProgram(s string) (Program, error) {
	if p, ok := stringToProgram[s]; ok {
		return p, nil
	}

	return ProgramUnknown, fmt.Errorf("%w%q", errUnknownProgram, s)
}

// Exec returns an executable name of the Program.
func (p Program) Exec() string {
	if s, ok := programToExec[p]; ok {
		return s
	}

	return fmt.Sprintf("unknown(%d)", p)
}

// GitRepo returns the source repository URL, or an empty string for other programs.
func (p Program) GitRepo() string {
	return programToGitRepo[p]
}

// BundleSource identifies the archive supplying standalone installations.
// Tarantool EE is extracted from SDKs; tt-ee and TCM have independent archives.
// Products built from Git have no bundle source.
func (p Program) BundleSource() Program {
	switch p {
	case ProgramEe, ProgramSDK:
		return ProgramSDK
	case ProgramTcm, ProgramTtEe:
		return p
	default:
		return ProgramUnknown
	}
}

// IsStandaloneArchive reports whether the product archive contains its executable
// directly at the archive root, independently of an SDK installation.
func (p Program) IsStandaloneArchive() bool {
	return p == ProgramTcm || p == ProgramTtEe
}

// CanBeSDKComponent reports whether a product can belong to an SDK installation.
// SDKProducts selects the components currently installed from the archive.
func (p Program) CanBeSDKComponent() bool {
	switch p {
	case ProgramEe, ProgramTcm, ProgramTtEe:
		return true
	default:
		return false
	}
}

// IsTarantool checks if the Program is kind of Tarantool program.
func (p Program) IsTarantool() bool {
	return p == ProgramCe || p == ProgramEe
}

// FallbackProgram identifies the community product sharing an Enterprise executable.
func (p Program) FallbackProgram() Program {
	switch p {
	case ProgramEe:
		return ProgramCe
	case ProgramTtEe:
		return ProgramTt
	default:
		return ProgramUnknown
	}
}

// EnterpriseProgram identifies the Enterprise edition of a community product.
func (p Program) EnterpriseProgram() Program {
	switch p {
	case ProgramCe:
		return ProgramEe
	case ProgramTt:
		return ProgramTtEe
	default:
		return ProgramUnknown
	}
}
