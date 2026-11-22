package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3/scope"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// probeRockspec is a rock whose build runs tarantool by name, as the cmake of
// a rock that looks for it with find_program does.
const probeRockspec = `package = 'probe'
version = 'scm-1'
source = { url = 'file://%s' }
build = {
   type = 'command',
   build_command = 'tarantool probe',
   install = { lua = { probe = 'probe.lua' } },
}
`

// TestRun_rockRunsTheTarantoolOfTheProject pins that the build of a
// dependency rock that runs tarantool runs the one of the project,
// .tarantool/bin/tarantool, as the build of a component does, with no
// tarantool in PATH and no .env/activate.sh sourced: found in the directory
// of the link, not in the one of the version it selects, which the Tarantool
// of the build names.
func TestRun_rockRunsTheTarantoolOfTheProject(t *testing.T) {
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	// PATH holds sh, which runs the command of the rock, and no tarantool.
	tools := t.TempDir()
	require.NoError(t, os.Symlink(sh, filepath.Join(tools, "sh")))
	t.Setenv("PATH", tools)

	base := t.TempDir()
	app := filepath.Join(base, "app")
	probe := filepath.Join(base, "probe")
	ran := filepath.Join(base, "ran")

	root := scope.ProjectRoot(app)
	exe := filepath.Join(root, "versions", "tarantool", "tarantool_3.9.0", "tarantool")
	writeFile(t, exe, "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> '"+ran+"'\n")
	require.NoError(t, os.Chmod(exe, 0o700))

	link := filepath.Join(ProjectBinDir(app), "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o750))
	require.NoError(t, os.Symlink(exe, link))

	writeFile(t, filepath.Join(app, manifestFileName), strings.Replace(consumerManifest,
		"contrib = { source = 'path', path = '../contrib' }",
		"probe = { source = 'path', path = '../probe' }", 1))
	writeFile(t, filepath.Join(app, "VERSION"), "1.2.3\n")
	writeFile(t, filepath.Join(app, "init.lua"), "return require('probe')\n")
	writeFile(t, filepath.Join(probe, "probe-scm-1.rockspec"), fmt.Sprintf(probeRockspec, probe))
	writeFile(t, filepath.Join(probe, "probe.lua"), "return {}\n")

	opts := dryOptions(app)

	opts.Tarantool = rocks.TarantoolInfo{Executable: exe, Prefix: root, Version: "3.9.0"}

	require.NoError(t, Run(context.Background(), opts))

	told, err := os.ReadFile(ran)
	require.NoError(t, err)
	assert.Equal(t, link+" probe\n", string(told))
	assert.FileExists(t, filepath.Join(app, rocksDirName, "share", "tarantool", "probe.lua"))
}
