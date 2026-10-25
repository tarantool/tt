package running

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
)

// markerApp is the code of a version of the application that writes the
// version and app.cfg.marker of its config into the data directory, so that
// the test sees which version runs and which config it read.
const markerApp = `local fio = require('fio')
local function put(name, value)
    local path = fio.pathjoin(box.cfg.wal_dir, name)
    local file = fio.open(path, {'O_CREAT', 'O_WRONLY', 'O_TRUNC'}, tonumber('644', 8))
    file:write(value)
    file:close()
end
put('marker', tostring(require('config'):get('app.cfg.marker')))
put('version', '%s')
`

// markerFiles returns the files of version of the application: storage-1,
// restarted on failure, running app.lua with marker in its config.
func markerFiles(version, marker string) map[string]string {
	files := appFiles("{restart: on-failure}",
		"app:\n  file: app.lua\n  cfg:\n    marker: "+marker+"\n")

	files["app.lua"] = strings.Replace(markerApp, "%s", version, 1)

	return files
}

// marker returns what app.lua wrote of the config into the data directory.
func (started *startedInstance) marker() string {
	data, _ := os.ReadFile(filepath.Join(started.inst.WalDir, "marker"))

	return string(data)
}

// TestWatchdogRestartStaysOnItsVersion pins the restart after a crash: the
// watchdog starts the instance again from the version it started from,
// although apps/<app> has moved to another version meanwhile, and with the
// pair of that version read anew, so an edited config applies.
func TestWatchdogRestartStaysOnItsVersion(t *testing.T) {
	root, _ := setScopeRoots(t)
	installTarantoolLink(t, root)

	first, link := installVersion(t, root, "1.0.0", markerFiles("1", "one"))
	fixture := appFixture{scope: app.ScopeUser, root: root, dir: first, link: link}
	inst := fixture.instanceCtx(t)
	started := startInstance(t, &inst, "1")
	require.Equal(t, "one", started.marker())

	installVersion(t, root, "2.0.0", markerFiles("2", "two"))

	edited := markerFiles("1", "edited")[".templates/default/config.yaml"]
	writeFiles(t, first, map[string]string{".templates/default/config.yaml": edited})

	crashed := started.pid
	require.NoError(t, syscall.Kill(crashed, syscall.SIGKILL))

	restarted := assert.Eventually(t, func() bool {
		pid := readPid(filepath.Join(inst.RunDir, "tarantool.pid"))
		if pid != 0 && pid != crashed {
			started.pid = pid
		}

		return started.pid != crashed && started.marker() == "edited"
	}, 30*time.Second, 50*time.Millisecond)
	if !restarted {
		log, _ := os.ReadFile(inst.Log)
		require.FailNow(t, "the instance did not restart with the edited config", "%s", log)
	}

	assert.Equal(t, "1", started.version(), "the restart stays on its version")

	started.stop()
}

// TestExplicitRestartTakesActiveVersion pins the explicit restart, a stop
// and a start that resolves the application anew: it starts the instance on
// the version apps/<app> points to by then.
func TestExplicitRestartTakesActiveVersion(t *testing.T) {
	root, _ := setScopeRoots(t)
	installTarantoolLink(t, root)

	first, link := installVersion(t, root, "1.0.0", markerFiles("1", "one"))
	fixture := appFixture{scope: app.ScopeUser, root: root, dir: first, link: link}
	inst := fixture.instanceCtx(t)
	started := startInstance(t, &inst, "1")

	second, _ := installVersion(t, root, "2.0.0", markerFiles("2", "two"))
	second, err := filepath.EvalSymlinks(second)
	require.NoError(t, err)

	started.stop()

	insts, err := Collect(t.Context(), []app.Target{{App: fixtureApp, Instance: fixtureInst}},
		app.Options{Scope: app.ScopeUser, Start: t.TempDir()})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Equal(t, second, insts[0].AppDir)

	restarted := startInstance(t, &insts[0], "2")
	assert.Equal(t, "two", restarted.marker())

	restarted.stop()
}
