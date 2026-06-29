package version_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/version"
)

func TestGenerateVersionLua(t *testing.T) {
	t.Parallel()

	ver := version.Version{
		SemVer: "1.4.1-dev.3+gabc1234.dirty",
		Commit: "abc1234",
		Dirty:  true,
		Flavor: "ce",
	}
	builtAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	got := version.GenerateVersionLua(ver, builtAt)

	assert.Equal(t, `return {
    version  = "1.4.1-dev.3+gabc1234.dirty",
    commit   = "abc1234",
    dirty    = true,
    flavor   = "ce",
    built_at = "2026-06-15T12:00:00Z",
}
`, got)
}

func TestGenerateVersionLuaNormalizesTimeToUTC(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("MSK", 3*60*60)
	builtAt := time.Date(2026, 6, 15, 15, 0, 0, 0, loc)

	ver := version.Version{SemVer: "", Commit: "", Dirty: false, Flavor: "ee"}
	got := version.GenerateVersionLua(ver, builtAt)

	assert.Contains(t, got, `built_at = "2026-06-15T12:00:00Z"`)
	assert.Contains(t, got, `flavor   = "ee"`)
	assert.Contains(t, got, `dirty    = false`)
}

// TestGenerateVersionLuaToggle pins where the on/off switch lives: the generator
// always renders, and the build consults the manifest to decide whether to call
// it. Default is on; only an explicit false turns it off.
func TestGenerateVersionLuaToggle(t *testing.T) {
	t.Parallel()

	disabled := false
	enabled := true

	pkg := func(toggle *bool) manifest.Package {
		return manifest.Package{
			Name:               "x",
			Description:        "",
			License:            "",
			LicenseFiles:       nil,
			Include:            nil,
			Repository:         "",
			Authors:            nil,
			GenerateVersionLua: toggle,
		}
	}

	assert.True(t, pkg(nil).GenerateVersionLuaValue())
	assert.True(t, pkg(&enabled).GenerateVersionLuaValue())
	assert.False(t, pkg(&disabled).GenerateVersionLuaValue())
}
