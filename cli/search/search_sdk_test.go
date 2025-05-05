package search_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/search"
)

func TestGetApiPackage(t *testing.T) {
	tests := map[string]struct {
		input    search.Program
		expected string
	}{
		"tarantool enterprise edition": {
			input:    search.ProgramEe,
			expected: "enterprise",
		},

		"tcm": {
			input:    search.ProgramTcm,
			expected: "tarantool-cluster-manager",
		},

		"tarantool development": {
			input:    search.ProgramDev,
			expected: "",
		},

		"tarantool community edition": {
			input:    search.ProgramCe,
			expected: "",
		},

		"tt cli": {
			input:    search.ProgramTt,
			expected: "",
		},

		"unknown program": {
			input:    search.ProgramUnknown,
			expected: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := search.GetApiPackage(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}
