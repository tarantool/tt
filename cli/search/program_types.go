package search

import "fmt"

// Program represents a strictly typed enum for program types.
type Program int

const (
	ProgramUnknown Program = iota
	ProgramCe              // tarantool
	ProgramEe              // tarantool-ee
	ProgramTt              // tt
	ProgramDev             // tarantool-dev
	ProgramTcm             // tcm
)

// programToExec contains executables matched for each Program types.
var programToExec = map[Program]string{
	ProgramCe:  "tarantool",
	ProgramEe:  "tarantool",
	ProgramTt:  "tt",
	ProgramDev: "tarantool",
	ProgramTcm: "tcm",
}

// programToString contains string representations for each type.
var programToString = map[Program]string{
	ProgramCe:  "tarantool",
	ProgramEe:  "tarantool-ee",
	ProgramDev: "tarantool-dev",
	ProgramTt:  "tt",
	ProgramTcm: "tcm",
}

// stringToProgram contains the reverse mapping for efficient lookup.
var stringToProgram = make(map[string]Program, len(programToString))

// init initialize the reverse map.
func init() {
	for k, v := range programToString {
		stringToProgram[v] = k
	}
}

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
	return ProgramUnknown, fmt.Errorf("unknown program: %q", s)
}

// Exec returns an executable name of the Program.
func (p Program) Exec() string {
	if s, ok := programToExec[p]; ok {
		return s
	}
	return fmt.Sprintf("unknown(%d)", p)
}

// IsTarantool checks if the Program is kind of Tarantool program.
func (p Program) IsTarantool() bool {
	return p == ProgramCe || p == ProgramEe || p == ProgramDev
}
