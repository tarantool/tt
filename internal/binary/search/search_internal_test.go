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

func TestReleaseTags(t *testing.T) {
	tags := []string{"3.8.0-entrypoint", "3.8.0", "3.9.0-rc1", "3.9.0-entrypoint"}
	versions := make(version.VersionSlice, 0, len(tags)+1)

	for _, tag := range tags {
		parsed, err := version.Parse(tag)
		require.NoError(t, err)

		versions = append(versions, parsed)
	}

	versions = append(versions, newMasterVersion())

	names := func(found version.VersionSlice) []string {
		result := make([]string, 0, len(found))
		for _, ver := range found {
			result = append(result, ver.Str)
		}

		return result
	}

	assert.Equal(t, []string{"3.8.0", "3.9.0-rc1", "master"}, names(releaseTags(versions, false)))
	assert.Equal(t, names(versions), names(releaseTags(versions, true)))
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
