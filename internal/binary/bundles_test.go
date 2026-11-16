package binary_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestCheckRequestedVersion(t *testing.T) {
	for _, test := range []struct {
		program   binary.Program
		requested string
		// unsupported is whether the request is a version tt does not install.
		unsupported bool
	}{
		{binary.ProgramCe, "2.11.5", true},
		{binary.ProgramCe, "1.10.15", true},
		{binary.ProgramCe, "2.11", true},
		{binary.ProgramCe, "2", true},
		{binary.ProgramCe, "2.10.0-rc1", true},
		{binary.ProgramCe, "2.11.0-entrypoint", true},
		{binary.ProgramEe, "2.11.1", true},
		{binary.ProgramEe, "gc64-2.11.1-0-r609", true},
		{binary.ProgramSDK, "sdk-gc64-2.11.1-0-r609", true},
		{binary.ProgramSDK, "2.11", true},
		{binary.ProgramCe, "3.0.0", false},
		{binary.ProgramCe, "3", false},
		{binary.ProgramCe, "", false},
		{binary.ProgramCe, "master", false},
		{binary.ProgramCe, "pr/123", false},
		// Commits, all-numeric or not.
		{binary.ProgramCe, "1234567", false},
		{binary.ProgramCe, "2345678abc", false},
		{binary.ProgramCe, "2abcdef", false},
		{binary.ProgramCe, "abcdef0", false},
		// tt, TCM and tt-ee have versions of their own.
		{binary.ProgramTt, "2.15.0", false},
		{binary.ProgramTtEe, "2.15.0", false},
		{binary.ProgramTcm, "1.3.1", false},
	} {
		t.Run(test.program.String()+"/"+test.requested, func(t *testing.T) {
			err := binary.CheckRequestedVersion(test.program, test.requested)
			if !test.unsupported {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, binary.ErrUnsupportedVersion)
			assert.EqualError(t, err, "unsupported version: "+test.program.String()+" "+
				test.requested+": tt 3 installs Tarantool 3.0 and later only; "+
				"use tt 2 to install Tarantool "+test.requested)
		})
	}
}
