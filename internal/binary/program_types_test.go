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
		"ProgramSDK":     {binary.ProgramSDK, "tarantool-sdk"},
		"ProgramTt":      {binary.ProgramTt, "tt"},
		"ProgramTtEe":    {binary.ProgramTtEe, "tt-ee"},
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
		"ValidCe":   {"tarantool", binary.ProgramCe, false, ""},
		"ValidEe":   {"tarantool-ee", binary.ProgramEe, false, ""},
		"ValidSDK":  {"tarantool-sdk", binary.ProgramSDK, false, ""},
		"ValidTt":   {"tt", binary.ProgramTt, false, ""},
		"ValidTtEe": {"tt-ee", binary.ProgramTtEe, false, ""},
		"RemovedDev": {
			"tarantool-dev", binary.ProgramUnknown, true, `unknown program: "tarantool-dev"`,
		},
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
		"ProgramTtEe":    {binary.ProgramTtEe, "tt"},
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

func TestProgram_GitRepo(t *testing.T) {
	repositories := map[binary.Program]string{
		binary.ProgramCe:      "https://github.com/tarantool/tarantool.git",
		binary.ProgramTt:      "https://github.com/tarantool/tt.git",
		binary.ProgramTtEe:    "",
		binary.ProgramEe:      "",
		binary.ProgramTcm:     "",
		binary.ProgramSDK:     "",
		binary.ProgramUnknown: "",
		binary.Program(99):    "",
	}
	for program, expected := range repositories {
		t.Run(program.String(), func(t *testing.T) {
			assert.Equal(t, expected, program.GitRepo())
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
		"ProgramTt":      {binary.ProgramTt, false},
		"ProgramTtEe":    {binary.ProgramTtEe, false},
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

func TestProgram_BundleSource(t *testing.T) {
	for program, source := range map[binary.Program]binary.Program{
		binary.ProgramEe:      binary.ProgramSDK,
		binary.ProgramSDK:     binary.ProgramSDK,
		binary.ProgramTcm:     binary.ProgramTcm,
		binary.ProgramTtEe:    binary.ProgramTtEe,
		binary.ProgramTt:      binary.ProgramUnknown,
		binary.ProgramCe:      binary.ProgramUnknown,
		binary.ProgramUnknown: binary.ProgramUnknown,
	} {
		t.Run(program.String(), func(t *testing.T) {
			assert.Equal(t, source, program.BundleSource())
		})
	}

	assert.True(t, binary.ProgramTtEe.CanBeSDKComponent())
}

func TestIsProductName(t *testing.T) {
	for _, name := range []string{
		"tarantool", "tarantool-ee", "tarantool-sdk", "tt", "tt-ee", "tcm",
	} {
		assert.True(t, binary.IsProductName(name), name)
	}

	// The aliases of the SDK name no directory, and neither does anything else.
	for _, name := range []string{"sdk", "SDK", "my-app", "", "unknown(0)"} {
		assert.False(t, binary.IsProductName(name), name)
	}
}
