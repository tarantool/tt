package install

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestTarantoolDockerRunOptions(t *testing.T) {
	scopes := []binary.Scope{binary.ScopeProject, binary.ScopeUser, binary.ScopeSystem}
	for _, scope := range scopes {
		t.Run(scope.String(), func(t *testing.T) {
			artifactRoot := filepath.Join(t.TempDir(), scope.String())
			installCtx := InstallCtx{Scope: scope}
			opts := tarantoolDockerRunOptions("build-context", "3.0.0", artifactRoot,
				installCtx)
			assert.Equal(t, []string{
				"./tt", "binary", "--scope", "project", "install", "-f", "tarantool", "3.0.0",
				"--reinstall",
			}, opts.Command)
			assert.Equal(t, []string{
				filepath.Join(artifactRoot, "versions") + ":/work/.tarantool/versions",
			}, opts.Binds)
			assert.Equal(t, "build-context", opts.BuildCtxDir)
			assert.False(t, opts.Verbose)
		})
	}

	t.Run("build flags", func(t *testing.T) {
		installCtx := InstallCtx{
			Scope:     binary.ScopeUser,
			Verbose:   true,
			Reinstall: true,
			LocalRepo: "/source files",
			Dynamic:   true,
		}
		opts := tarantoolDockerRunOptions("build-context", "master", "user-root",
			installCtx)
		assert.Equal(t, []string{
			"./tt", "binary", "--scope", "project", "-V", "install", "-f", "tarantool", "master",
			"--reinstall", "--local-repo", "/tt_distfiles", "--dynamic",
		}, opts.Command)
		assert.Equal(t, []string{
			"user-root/versions:/work/.tarantool/versions",
			"/source files:/tt_distfiles",
		}, opts.Binds)
		assert.True(t, opts.Verbose)
	})
}
