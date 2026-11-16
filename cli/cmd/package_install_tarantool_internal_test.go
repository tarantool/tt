package cmd

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/manifest/install"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

// TestMissingTarantoolWarnings checks which installed applications are
// looked a tarantool up for, in which directory, and what the warning says.
//
//nolint:paralleltest // Replaces resolveTarantool and the scope roots.
func TestMissingTarantoolWarnings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TT_USER_ROOT", root)

	previous := resolveTarantool

	t.Cleanup(func() { resolveTarantool = previous })

	var asked []tarantool.App

	resolveTarantool = func(app tarantool.App) (tarantool.Executable, error) {
		asked = append(asked, app)
		if filepath.Base(app.Dir) == "ok_1.0.0" {
			return tarantool.Executable{Path: "/bin/tarantool"}, nil
		}

		return tarantool.Executable{}, errors.New("tarantool is not installed in the user scope")
	}

	warnings := missingTarantoolWarnings([]install.OneResult{
		{Package: "guest", Version: "1.0.0", Scope: install.ScopeProject},
		{Package: "same", Version: "1.0.0", Scope: install.ScopeUser, Skipped: true},
		{Package: "ok", Version: "1.0.0", Scope: install.ScopeUser},
		{Package: "hello", Version: "1.1.0", Scope: install.ScopeUser},
	}, t.TempDir())

	assert.Equal(t, []string{
		"hello 1.1.0 cannot start yet: tarantool is not installed in the user scope",
	}, warnings)
	assert.Equal(t, []tarantool.App{
		{Scope: binary.ScopeUser, Dir: filepath.Join(root, "versions", "ok", "ok_1.0.0")},
		{Scope: binary.ScopeUser, Dir: filepath.Join(root, "versions", "hello", "hello_1.1.0")},
	}, asked, "a guest of a project and an upgrade that did nothing are not looked at")
}
