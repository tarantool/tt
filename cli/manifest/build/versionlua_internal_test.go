package build

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

func TestWriteVersionLua_generates(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()
	ver := version.Version{SemVer: "1.2.3", Commit: "abc1234", Flavor: "ce"}

	err := writeVersionLua(stage, "my-app", true, ver, time.Unix(0, 0), nil)
	require.NoError(t, err)

	vlua := filepath.Join(stage, "lua/my-app/version.lua")
	data, err := os.ReadFile(vlua)
	require.NoError(t, err)
	assert.Contains(t, string(data), `version  = "1.2.3"`)
	assert.Contains(t, string(data), `flavor   = "ce"`)
}

func TestWriteVersionLua_disabledWritesNothing(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()

	err := writeVersionLua(stage, "my-app", false, version.Version{}, time.Unix(0, 0), nil)
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(stage, "lua/my-app/version.lua"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestWriteVersionLua_collisionWithLaidOutFile(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()
	dst := filepath.Join(stage, "lua/my-app/version.lua")

	// A component laid a version.lua at the exact generated path this run.
	err := writeVersionLua(stage, "my-app", true, version.Version{}, time.Unix(0, 0), []string{dst})
	require.Error(t, err)
	require.ErrorIs(t, err, errVersionLuaCollision)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

func TestWriteVersionLua_staleFileOverwritten(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()
	dst := filepath.Join(stage, "lua/my-app/version.lua")
	writeFile(t, dst, "-- stale from a previous build")

	// The stale file is not in laidOut, so it is overwritten, not a collision.
	err := writeVersionLua(stage, "my-app", true,
		version.Version{SemVer: "9.9.9"}, time.Unix(0, 0), nil)
	require.NoError(t, err)

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Contains(t, string(data), `version  = "9.9.9"`)
}
