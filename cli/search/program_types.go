package search

import "fmt"

// ProgramType represents a strictly typed enum for program types.
type ProgramType int

const (
	ProgramUnknown ProgramType = iota
	ProgramCe                  // tarantool
	ProgramEe                  // tarantool-ee
	ProgramTt                  // tt
	ProgramDev                 // tarantool-dev
	ProgramTcm                 // tcm
)

// programTypeToExec contains executables matched for each ProgramType.
var programTypeToExec = map[ProgramType]string{
	ProgramCe:  "tarantool",
	ProgramEe:  "tarantool",
	ProgramTt:  "tt",
	ProgramDev: "tarantool",
	ProgramTcm: "tcm",
}

// programTypeToString contains string representations for each type.
var programTypeToString = map[ProgramType]string{
	ProgramCe:  "tarantool",
	ProgramEe:  "tarantool-ee",
	ProgramDev: "tarantool-dev",
	ProgramTt:  "tt",
	ProgramTcm: "tcm",
}

// stringToProgramType contains the reverse mapping for efficient lookup.
var stringToProgramType = make(map[string]ProgramType, len(programTypeToString))

// init initialize the reverse map.
func init() {
	for k, v := range programTypeToString {
		stringToProgramType[v] = k
	}
}

// String returns a string representation of ProgramType.
func (p ProgramType) String() string {
	if s, ok := programTypeToString[p]; ok {
		return s
	}
	return fmt.Sprintf("unknown(%d)", p)
}

// NewProgramType converts the string to ProgramType.
func NewProgramType(s string) ProgramType {
	if p, ok := stringToProgramType[s]; ok {
		return p
	}
	return ProgramUnknown
}

// Exec returns an executable name of the ProgramType.
func (p ProgramType) Exec() string {
	if s, ok := programTypeToExec[p]; ok {
		return s
	}
	return fmt.Sprintf("unknown(%d)", p)
}

// IsTarantool checks if the ProgramType is a Tarantool program.
func (p ProgramType) IsTarantool() bool {
	return p == ProgramCe || p == ProgramEe || p == ProgramDev
}
