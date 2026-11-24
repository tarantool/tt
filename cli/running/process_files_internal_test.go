package running

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/instances"
)

// configuredInstance returns the context of the instance i1 of the cluster
// config config, which runs from appDir and logs next to logDir.
func configuredInstance(t *testing.T, config, appDir, logDir string) *InstanceCtx {
	t.Helper()

	insts := validatedInstances(t, "proc", config, "i1")
	require.Len(t, insts, 1)

	inst := insts[0]

	inst.AppDir = appDir
	inst.LogDir = logDir

	return &inst
}

// instanceConfig is a cluster config of the instance i1 with conf as its
// own config, indented as an instance of it.
func instanceConfig(conf string) string {
	return "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          i1:\n" + conf
}

// TestWorkDir pins where the process of an instance runs: the application
// directory, or process.work_dir of the config, or TT_WORK_DIR of the
// record of a script, relative to it; the workdir of an external process.
//
//nolint:paralleltest // Reads the TT_* environment.
func TestWorkDir(t *testing.T) {
	appDir := t.TempDir()

	inst := configuredInstance(t, instanceConfig("            {}\n"), appDir, "/log")
	assert.Equal(t, appDir, WorkDir(inst))

	inst = configuredInstance(t, instanceConfig("            process:\n"+
		"              work_dir: data/{{ instance_name }}\n"), appDir, "/log")
	assert.Equal(t, filepath.Join(appDir, "data", "i1"), WorkDir(inst))

	inst = configuredInstance(t, instanceConfig("            process:\n"+
		"              work_dir: /srv/i1\n"), appDir, "/log")
	assert.Equal(t, "/srv/i1", WorkDir(inst))

	// The env of the record ranks above the config, as tarantool ranks it.
	inst.Env = map[string]string{"TT_PROCESS_WORK_DIR": "/srv/env"}
	assert.Equal(t, "/srv/env", WorkDir(inst))

	script := &InstanceCtx{Kind: instances.KindScript, AppDir: appDir}
	assert.Equal(t, appDir, WorkDir(script))

	script.Env = map[string]string{"TT_WORK_DIR": "work"}
	assert.Equal(t, filepath.Join(appDir, "work"), WorkDir(script))

	external := &InstanceCtx{Kind: instances.KindExternal, AppDir: appDir, Workdir: "/srv/ext"}
	assert.Equal(t, "/srv/ext", WorkDir(external))
}

// TestTarantoolLog pins the file a Tarantool instance logs into of its own:
// none while it logs to its standard error, into tt.log, as by default.
//
//nolint:paralleltest // Reads the TT_* environment.
func TestTarantoolLog(t *testing.T) {
	appDir := t.TempDir()
	logDir := filepath.Join(appDir, "var", "log", "app", "i1")

	inst := configuredInstance(t, instanceConfig("            {}\n"), appDir, logDir)
	assert.Empty(t, TarantoolLog(inst))

	inst = configuredInstance(t, instanceConfig("            log:\n"+
		"              to: syslog\n"), appDir, logDir)
	assert.Empty(t, TarantoolLog(inst))

	// tt gives log.file a default of its own, next to tt.log.
	inst = configuredInstance(t, instanceConfig("            log:\n"+
		"              to: file\n"), appDir, logDir)
	assert.Equal(t, filepath.Join(logDir, "tarantool.log"), TarantoolLog(inst))

	inst = configuredInstance(t, instanceConfig("            log:\n"+
		"              to: file\n              file: logs/{{ instance_name }}.log\n"+
		"            process:\n              work_dir: /srv/i1\n"), appDir, logDir)
	assert.Equal(t, "/srv/i1/logs/i1.log", TarantoolLog(inst))

	script := &InstanceCtx{Kind: instances.KindScript, AppDir: appDir}
	assert.Empty(t, TarantoolLog(script))

	for value, want := range map[string]string{
		"file:/var/log/i1.log": "/var/log/i1.log",
		"i1.log":               filepath.Join(appDir, "i1.log"),
		"pipe:cronolog x":      "",
		"| cronolog x":         "",
		"syslog:identity=tt":   "",
	} {
		script.Env = map[string]string{"TT_LOG": value}
		assert.Equal(t, want, TarantoolLog(script), value)
	}

	external := &InstanceCtx{Kind: instances.KindExternal, AppDir: appDir, LogDir: logDir}
	assert.Empty(t, TarantoolLog(external))
}
