package binary_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestGetApiPackage(t *testing.T) {
	tests := map[string]struct {
		input    binary.Program
		expected string
	}{
		"tarantool enterprise edition": {
			input:    binary.ProgramEe,
			expected: "enterprise",
		},

		"tcm": {
			input:    binary.ProgramTcm,
			expected: "tarantool-cluster-manager",
		},

		"tarantool development": {
			input:    binary.ProgramDev,
			expected: "",
		},

		"tarantool community edition": {
			input:    binary.ProgramCe,
			expected: "",
		},

		"tt cli": {
			input:    binary.ProgramTt,
			expected: "",
		},

		"unknown program": {
			input:    binary.ProgramUnknown,
			expected: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := binary.GetAPIPackage(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}
