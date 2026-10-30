package running

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
)

// TestApplyConfigVars pins the substitution of the variables of the config:
// spaces inside the braces are allowed, and a variable it does not have is
// left as it is.
func TestApplyConfigVars(t *testing.T) {
	vars := map[string]string{"instance_name": "i1", "replicaset_name": "r", "group_name": "g"}

	assert.Equal(t, "/d/g/r/i1", applyConfigVars("/d/{{group_name}}/{{ replicaset_name }}/"+
		"{{  instance_name  }}", vars))
	assert.Equal(t, "/d/{{ context.root }}/i1",
		applyConfigVars("/d/{{ context.root }}/{{ instance_name }}", vars))
	assert.Equal(t, "/d/{{ instance_name }}", applyConfigVars("/d/{{ instance_name }}", nil))
}

// TestNewInstanceCtxConfigVars pins that the paths the config of an instance
// sets get every variable tarantool substitutes - the instance, replicaset
// and group names - and that the first unix socket of iproto.listen is the
// binary port.
func TestNewInstanceCtxConfigVars(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, appFiles("",
		"console:\n  socket: /run/{{ group_name }}/{{ replicaset_name }}/"+
			"{{ instance_name }}.control\n"+
			"snapshot:\n  dir: /data/{{ replicaset_name }}/{{ instance_name }}\n"+
			"iproto:\n  listen:\n  - uri: 127.0.0.1:3301\n"+
			"  - uri: unix/:./run/{{ instance_name }}.sock\n"))
	inst := fixture.instanceCtx(t)

	assert.Equal(t, "/run/g/r/storage-1.control", inst.ConsoleSocket)
	assert.Equal(t, "/data/r/storage-1", inst.MemtxDir)
	assert.Equal(t, filepath.Join(fixture.dir, "run", "storage-1.sock"), inst.BinaryPort)
}

// TestNewInstanceCtxBinaryPortOfLayout pins that the binary port stays the
// one of the layout when iproto.listen has no unix socket, and when the env
// of the record sets TT_IPROTO_LISTEN, which ranks above the config.
func TestNewInstanceCtxBinaryPortOfLayout(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"tcp only": appFiles("", "iproto:\n  listen:\n  - uri: 127.0.0.1:3301\n"),
		"record env": appFiles("{env: {TT_IPROTO_LISTEN: '127.0.0.1:3302'}}",
			"iproto:\n  listen:\n  - uri: unix/:/sock/x.sock\n"),
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newAppFixture(t, app.ScopeProject, files)
			inst := fixture.instanceCtx(t)

			assert.Equal(t, filepath.Join(fixture.root, "var", "run", fixtureApp, fixtureInst,
				"tarantool.sock"), inst.BinaryPort)
		})
	}
}

// relativeLayoutConfig puts every file of an installed instance into the
// layout of its scope root by paths relative to its version directory,
// <root>/versions/<app>/<app>_<version>.
const relativeLayoutConfig = "wal:\n  dir: ../../../var/lib/myapp/{{ instance_name }}\n" +
	"snapshot:\n  dir: ../../../var/lib/myapp/{{ instance_name }}\n" +
	"vinyl:\n  dir: ../../../var/lib/myapp/{{ instance_name }}\n" +
	"process:\n  pid_file: ../../../var/run/myapp/{{ instance_name }}/tarantool.pid\n" +
	"console:\n  socket: ../../../var/run/myapp/{{ instance_name }}/tarantool.control\n" +
	"log:\n  file: ../../../var/log/myapp/{{ instance_name }}/tarantool.log\n" +
	"iproto:\n  listen:\n  - uri: unix/:./../../../var/run/myapp/{{ instance_name }}/" +
	"tarantool.sock\n"

// TestRelativePathsOutOfVersion pins that relative paths that lead an
// installed instance out of its version directory are let through and
// resolve where tarantool resolves them: to the layout of the scope root.
func TestRelativePathsOutOfVersion(t *testing.T) {
	for _, appScope := range []app.Scope{app.ScopeUser, app.ScopeSystem} {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, appFiles("", relativeLayoutConfig))
			fixture.installTarantool(t, fixtureTarantool)

			inst := fixture.instanceCtx(t)
			require.NoError(t, checkStart(&inst))

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, fixtureInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, fixtureInst)

			assert.Equal(t, data, inst.WalDir)
			assert.Equal(t, data, inst.MemtxDir)
			assert.Equal(t, data, inst.VinylDir)
			assert.Equal(t, filepath.Join(run, "tarantool.control"), inst.ConsoleSocket)
			assert.Equal(t, filepath.Join(run, "tarantool.sock"), inst.BinaryPort)
		})
	}
}
