package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestIsMasked(t *testing.T) {
	old, err := version.Parse("gc64-2.11.1-0-r563")
	require.NoError(t, err)

	current, err := version.Parse("gc64-3.0.0-0-r1")
	require.NoError(t, err)

	for _, program := range []binary.Program{
		binary.ProgramCe, binary.ProgramEe, binary.ProgramSDK,
	} {
		assert.True(t, isMasked(program, old), program.String())
		assert.False(t, isMasked(program, current), program.String())
	}

	// tt, tt-ee and TCM have versions of their own.
	for _, program := range []binary.Program{
		binary.ProgramTt, binary.ProgramTtEe, binary.ProgramTcm,
	} {
		assert.False(t, isMasked(program, old), program.String())
	}
}

func TestNewSearchCtx(t *testing.T) {
	got := NewSearchCtx()
	assert.Equal(t, SearchRelease, got.Filter)
	assert.Equal(t, binary.ProgramUnknown, got.Program)
	assert.Empty(t, got.LocalRepo)
	assert.Empty(t, got.ReleaseVersion)
	assert.False(t, got.DevBuilds)
	assert.False(t, got.Debug)
}
