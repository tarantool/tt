package binary_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestProgram_String(t *testing.T) {
	tests := map[string]struct {
		program  binary.Program
		expected string
	}{
		"ProgramCe":      {binary.ProgramCe, "tarantool"},
		"ProgramEe":      {binary.ProgramEe, "tarantool-ee"},
		"ProgramTt":      {binary.ProgramTt, "tt"},
		"ProgramDev":     {binary.ProgramDev, "tarantool-dev"},
		"ProgramTcm":     {binary.ProgramTcm, "tcm"},
		"ProgramUnknown": {binary.ProgramUnknown, "unknown(0)"},
		"InvalidProgram": {binary.Program(99), "unknown(99)"},
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
		expected binary.Program
		wantErr  bool
		errMsg   string
	}{
		"ValidCe":  {"tarantool", binary.ProgramCe, false, ""},
		"ValidEe":  {"tarantool-ee", binary.ProgramEe, false, ""},
		"ValidTt":  {"tt", binary.ProgramTt, false, ""},
		"ValidDev": {"tarantool-dev", binary.ProgramDev, false, ""},
		"ValidTcm": {"tcm", binary.ProgramTcm, false, ""},
		"InvalidProgram": {
			"unknown-program", binary.ProgramUnknown, true, `unknown program: "unknown-program"`,
		},
		"EmptyString": {"", binary.ProgramUnknown, true, `unknown program: ""`},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			program, err := binary.ParseProgram(test.input)
			if test.wantErr {
				require.Error(t, err)

				if test.errMsg != "" {
					require.EqualError(t, err, test.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.expected, program)
			}
		})
	}
}

func TestProgram_Exec(t *testing.T) {
	tests := map[string]struct {
		program  binary.Program
		expected string
	}{
		"ProgramCe":      {binary.ProgramCe, "tarantool"},
		"ProgramEe":      {binary.ProgramEe, "tarantool"},
		"ProgramTt":      {binary.ProgramTt, "tt"},
		"ProgramDev":     {binary.ProgramDev, "tarantool"},
		"ProgramTcm":     {binary.ProgramTcm, "tcm"},
		"ProgramUnknown": {binary.ProgramUnknown, "unknown(0)"},
		"InvalidProgram": {binary.Program(99), "unknown(99)"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.program.Exec())
		})
	}
}

func TestProgram_IsTarantool(t *testing.T) {
	tests := map[string]struct {
		program  binary.Program
		expected bool
	}{
		"ProgramCe":      {binary.ProgramCe, true},
		"ProgramEe":      {binary.ProgramEe, true},
		"ProgramDev":     {binary.ProgramDev, true},
		"ProgramTt":      {binary.ProgramTt, false},
		"ProgramTcm":     {binary.ProgramTcm, false},
		"ProgramUnknown": {binary.ProgramUnknown, false},
		"InvalidProgram": {binary.Program(99), false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.program.IsTarantool())
		})
	}
}
