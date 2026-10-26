package running

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/systemd"
)

// TestAttachUnits pins that the instances of an application of the system
// scope get the units its mark names, and the others none: an instance the
// mark does not name, an application without a mark, and other scopes, which
// have no units.
func TestAttachUnits(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, systemd.WriteMark(app.HostDir(root, "myapp"), systemd.Mark{
		UnitDir:   "/etc/systemd/system",
		Template:  "myapp@.service",
		Instances: map[string]string{"a": "myapp@a.service"},
	}))

	insts := []InstanceCtx{
		{AppName: "myapp", InstName: "a", Scope: app.ScopeSystem, Root: root},
		{AppName: "myapp", InstName: "b", Scope: app.ScopeSystem, Root: root},
		{AppName: "other", InstName: "a", Scope: app.ScopeSystem, Root: root},
		{AppName: "myapp", InstName: "a", Scope: app.ScopeUser, Root: root},
		{AppName: "myapp", InstName: "a", Scope: app.ScopeProject, Root: root},
	}

	require.NoError(t, AttachUnits(insts))

	units := make([]string, 0, len(insts))
	for _, inst := range insts {
		units = append(units, inst.Unit)
	}

	assert.Equal(t, []string{"myapp@a.service", "", "", "", ""}, units)

	require.NoError(t, os.WriteFile(systemd.MarkPath(app.HostDir(root, "myapp")),
		[]byte("instances: [\n"), 0o644))

	err := AttachUnits(insts[:1])
	require.ErrorIs(t, err, systemd.ErrInvalidMark)
}

// TestStopTimeouts pins the stop timeouts the units are made with: the one
// of the record or the default, and the margin of tt instance stop.
func TestStopTimeouts(t *testing.T) {
	assert.Equal(t, 30*time.Second, StopTimeoutOf(&InstanceCtx{}))
	assert.Equal(t, time.Minute, StopTimeoutOf(&InstanceCtx{StopTimeout: time.Minute}))
	assert.Equal(t, 5*time.Second, StopMargin)

	pidFile, err := PIDFile("/opt/tarantool", "myapp", "a")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/opt/tarantool", "var", "run", "myapp", "a", "tt.pid"),
		pidFile)
}
