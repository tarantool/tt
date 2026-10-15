package binary_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestParseVersionRequest(t *testing.T) {
	for _, testCase := range []struct {
		requested string
		program   binary.Program
		version   string
	}{
		{"", binary.ProgramCe, ""},
		{"3", binary.ProgramCe, "3"},
		{"3.8", binary.ProgramCe, "3.8"},
		{"3.8.0", binary.ProgramCe, "3.8.0"},
		{"3ce", binary.ProgramCe, "3"},
		{"3.8ce", binary.ProgramCe, "3.8"},
		{"3.8.0ce", binary.ProgramCe, "3.8.0"},
		{"3ee", binary.ProgramEe, "3"},
		{"3.8ee", binary.ProgramEe, "3.8"},
		{"3.8.0ee", binary.ProgramEe, "3.8.0"},
		{"3.8.0-rc1ee", binary.ProgramEe, "3.8.0-rc1"},
		{"3.8.0-0-r94ee", binary.ProgramEe, "3.8.0-0-r94"},
		{"v3.8.0ce", binary.ProgramCe, "v3.8.0"},
		{"gc64-3.8.0-0-r94", binary.ProgramEe, "gc64-3.8.0-0-r94"},
		{"nogc64-2.11.9", binary.ProgramEe, "nogc64-2.11.9"},
		{"debug-gc64-3.8.0-0-r94", binary.ProgramEe, "debug-gc64-3.8.0-0-r94"},
		{"debug-3.8.0", binary.ProgramEe, "debug-3.8.0"},
		{"sdk-gc64-3.8.0-0-r94", binary.ProgramEe, "sdk-gc64-3.8.0-0-r94"},
		{"master", binary.ProgramCe, "master"},
		{"abcdef0", binary.ProgramCe, "abcdef0"},
		{"abcdefce", binary.ProgramCe, "abcdefce"},
		{"12345ee", binary.ProgramCe, "12345ee"},
		{"1234567", binary.ProgramCe, "1234567"},
		{"pr/123", binary.ProgramCe, "pr/123"},
		{"featureee", binary.ProgramCe, "featureee"},
		{"3..8ee", binary.ProgramCe, "3..8ee"},
	} {
		t.Run(testCase.requested, func(t *testing.T) {
			request, err := binary.ParseVersionRequest("tarantool", testCase.requested)
			require.NoError(t, err)
			assert.Equal(t, binary.VersionRequest{
				Program: testCase.program, Version: testCase.version,
			}, request)
		})
	}

	for _, program := range []string{"tt-ee", "tarantool-ee", "tcm", "sdk"} {
		t.Run(program, func(t *testing.T) {
			request, err := binary.ParseVersionRequest(program, "3.8.0ee")
			require.NoError(t, err)

			parsed, err := binary.ParseProgram(program)
			require.NoError(t, err)
			assert.Equal(t, binary.VersionRequest{Program: parsed, Version: "3.8.0ee"}, request)
		})
	}

	_, err := binary.ParseVersionRequest("unknown", "3.8.0")
	require.ErrorContains(t, err, "unknown program")
}

func TestParseTtVersionRequest(t *testing.T) {
	for _, testCase := range []struct {
		requested string
		program   binary.Program
		version   string
	}{
		{"", binary.ProgramTt, ""},
		{"2", binary.ProgramTt, "2"},
		{"2.15", binary.ProgramTt, "2.15"},
		{"2.15.0", binary.ProgramTt, "2.15.0"},
		{"2ce", binary.ProgramTt, "2"},
		{"2.15ce", binary.ProgramTt, "2.15"},
		{"2.15.0ce", binary.ProgramTt, "2.15.0"},
		{"2ee", binary.ProgramTtEe, "2"},
		{"2.15ee", binary.ProgramTtEe, "2.15"},
		{"2.15.0ee", binary.ProgramTtEe, "2.15.0"},
		{"2.15.0-rc1ee", binary.ProgramTtEe, "2.15.0-rc1"},
		{"v2.15.0ce", binary.ProgramTt, "v2.15.0"},
		{"master", binary.ProgramTt, "master"},
		{"abcdef0", binary.ProgramTt, "abcdef0"},
		{"abcdefce", binary.ProgramTt, "abcdefce"},
		{"12345ee", binary.ProgramTt, "12345ee"},
		{"1234567", binary.ProgramTt, "1234567"},
		{"pr/123", binary.ProgramTt, "pr/123"},
		{"sdk-gc64-3.8.0-0-r94", binary.ProgramTtEe, "sdk-gc64-3.8.0-0-r94"},
		{"gc64-3.8.0-0-r94", binary.ProgramTt, "gc64-3.8.0-0-r94"},
	} {
		t.Run(testCase.requested, func(t *testing.T) {
			request, err := binary.ParseVersionRequest("tt", testCase.requested)
			require.NoError(t, err)
			assert.Equal(t, binary.VersionRequest{
				Program: testCase.program, Version: testCase.version,
			}, request)
		})
	}
}

func TestVersionArgumentRoundTrip(t *testing.T) {
	for _, testCase := range []struct {
		program  binary.Program
		versions []string
	}{
		{binary.ProgramCe, []string{"3.8.0", "v3.8.0", "master", "abcdefce"}},
		{binary.ProgramEe, []string{
			"3.8.0", "3.8.0-rc1", "3.8.0-0-r94", "gc64-2.11.9",
			"debug-3.8.0", "sdk-gc64-3.8.0-0-r94",
		}},
		{binary.ProgramTt, []string{"2.15.0", "v2.15.0", "master", "abcdefce"}},
		{binary.ProgramTtEe, []string{"2.15.0", "2.15.0-rc1", "sdk-gc64-3.8.0-0-r94"}},
	} {
		for _, installed := range testCase.versions {
			t.Run(testCase.program.String()+"/"+installed, func(t *testing.T) {
				community := testCase.program
				if fallback := community.FallbackProgram(); fallback != binary.ProgramUnknown {
					community = fallback
				}

				argument := binary.VersionArgument(testCase.program, installed)
				request, err := binary.ParseVersionRequest(community.String(), argument)
				require.NoError(t, err)
				assert.Equal(t, binary.VersionRequest{
					Program: testCase.program, Version: installed,
				}, request)
			})
		}
	}
}
