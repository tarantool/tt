package running

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/pidfile"
)

// TestOrphanCtx pins that an instance that is not in the config is taken as
// its watchdog runs it: the paths of its launch, which the config no longer
// gives, rather than those of the layout; without a watchdog that runs it,
// as the layout gives it.
func TestOrphanCtx(t *testing.T) {
	dir := t.TempDir()
	layout := InstanceCtx{
		AppName:       "myapp",
		InstName:      "storage-2",
		Kind:          instances.KindCluster,
		RunDir:        dir,
		PIDFile:       filepath.Join(dir, "tt.pid"),
		ConsoleSocket: filepath.Join(dir, "tarantool.control"),
		Template:      "named",
	}

	orphan := orphanCtx(layout)
	assert.True(t, orphan.NotInConfig)
	assert.Equal(t, layout.ConsoleSocket, orphan.ConsoleSocket, "no watchdog runs it")

	owned, err := pidfile.Acquire(layout.PIDFile, os.Getpid())
	require.NoError(t, err)
	t.Cleanup(func() { _ = owned.Release() })

	started := layout

	started.ConsoleSocket = filepath.Join(dir, "custom.control")
	started.Log = filepath.Join(dir, "custom.log")
	started.Template = ""

	launch := Launch{
		Version:  launchVersion,
		Instance: started,
		Spec:     LaunchSpec{Path: "tarantool", Args: nil},
	}
	require.NoError(t, launch.publish(os.Getpid()))

	orphan = orphanCtx(layout)
	assert.True(t, orphan.NotInConfig)
	assert.Equal(t, started.ConsoleSocket, orphan.ConsoleSocket)
	assert.Equal(t, started.Log, orphan.Log)
	assert.Equal(t, "named", orphan.Template, "the template it was resolved with stays")
}
