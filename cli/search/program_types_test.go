package search_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/search"
)

func TestProgram_String(t *testing.T) {
	tests := map[string]struct {
		program  search.Program
		expected string
	}{
		"ProgramCe":      {search.ProgramCe, "tarantool"},
		"ProgramEe":      {search.ProgramEe, "tarantool-ee"},
		"ProgramTt":      {search.ProgramTt, "tt"},
		"ProgramDev":     {search.ProgramDev, "tarantool-dev"},
		"ProgramTcm":     {search.ProgramTcm, "tcm"},
		"ProgramUnknown": {search.ProgramUnknown, "unknown(0)"},
		"InvalidProgram": {search.Program(99), "unknown(99)"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.program.String())
		})
	}
}

func TestParseProgram(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected search.Program
		wantErr  bool
		errMsg   string
	}{
		"ValidCe":  {"tarantool", search.ProgramCe, false, ""},
		"ValidEe":  {"tarantool-ee", search.ProgramEe, false, ""},
		"ValidTt":  {"tt", search.ProgramTt, false, ""},
		"ValidDev": {"tarantool-dev", search.ProgramDev, false, ""},
		"ValidTcm": {"tcm", search.ProgramTcm, false, ""},
		"InvalidProgram": {
			"unknown-program", search.ProgramUnknown, true, `unknown program: "unknown-program"`,
		},
		"EmptyString": {"", search.ProgramUnknown, true, `unknown program: ""`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			program, err := search.ParseProgram(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				if tc.errMsg != "" {
					assert.EqualError(t, err, tc.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, program)
			}
		})
	}
}

func TestProgram_Exec(t *testing.T) {
	tests := map[string]struct {
		program  search.Program
		expected string
	}{
		"ProgramCe":      {search.ProgramCe, "tarantool"},
		"ProgramEe":      {search.ProgramEe, "tarantool"},
		"ProgramTt":      {search.ProgramTt, "tt"},
		"ProgramDev":     {search.ProgramDev, "tarantool"},
		"ProgramTcm":     {search.ProgramTcm, "tcm"},
		"ProgramUnknown": {search.ProgramUnknown, "unknown(0)"},
		"InvalidProgram": {search.Program(99), "unknown(99)"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.program.Exec())
		})
	}
}

func TestProgram_IsTarantool(t *testing.T) {
	tests := map[string]struct {
		program  search.Program
		expected bool
	}{
		"ProgramCe":      {search.ProgramCe, true},
		"ProgramEe":      {search.ProgramEe, true},
		"ProgramDev":     {search.ProgramDev, true},
		"ProgramTt":      {search.ProgramTt, false},
		"ProgramTcm":     {search.ProgramTcm, false},
		"ProgramUnknown": {search.ProgramUnknown, false},
		"InvalidProgram": {search.Program(99), false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.program.IsTarantool())
		})
	}
}
