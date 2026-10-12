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
)

// tarantoolname is the executable of every tarantool flavour and the name
// of the community edition.
const tarantoolName = "tarantool"

// programToExec contains executables matched for each Program types.
var programToExec = map[Program]string{
	ProgramCe:  tarantoolName,
	ProgramEe:  tarantoolName,
	ProgramTt:  "tt",
	ProgramTcm: "tcm",
}

// programToString contains string representations for each type.
var programToString = map[Program]string{
	ProgramCe:  tarantoolName,
	ProgramEe:  "tarantool-ee",
	ProgramTt:  "tt",
	ProgramTcm: "tcm",
	ProgramSDK: "tarantool-sdk",
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

// IsTarantool checks if the Program is kind of Tarantool program.
func (p Program) IsTarantool() bool {
	return p == ProgramCe || p == ProgramEe
}
