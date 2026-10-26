package systemd_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/systemd"
)

// update rewrites the golden files instead of comparing against them:
// go test ./internal/systemd -update.
var update = flag.Bool("update", false, "rewrite golden files")

// render joins files as tt service systemd generate prints them.
func render(files []systemd.File) string {
	var out strings.Builder

	for i, file := range files {
		if i > 0 {
			out.WriteString("\n")
		}

		out.WriteString("==> " + file.Name + " <==\n")
		out.Write(file.Content)
	}

	return out.String()
}

// compareGolden compares got with the golden file name in testdata.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// sampleUnits are the units of an application whose instances differ in
// restart and stop_timeout.
func sampleUnits() systemd.Units {
	return systemd.Units{
		App:         "myapp",
		RunAs:       systemd.RunAs{User: "tt-svc", Group: "tarantool"},
		Root:        "/app/tarantool",
		TT:          "/app/tarantool/bin/tt",
		StopTimeout: 35 * time.Second,
		Instances: []systemd.Instance{
			{Name: "router-1", Restart: instances.RestartNo, StopTimeout: 35 * time.Second},
			{
				Name: "storage-1", Restart: instances.RestartOnFailure,
				StopTimeout: 2*time.Minute + 5*time.Second,
			},
			{
				Name: "worker.a", Restart: instances.RestartAlways,
				StopTimeout: 6500 * time.Millisecond,
			},
		},
	}
}

// TestGenerateGolden pins the units of an application: the template, and a
// drop-in for every instance with its restart policy and stop timeout.
func TestGenerateGolden(t *testing.T) {
	files, err := systemd.Generate(sampleUnits())
	require.NoError(t, err)

	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)

		assert.True(t, systemd.IsGenerated(file.Content), file.Name)
	}

	assert.Equal(t, []string{
		"myapp@.service",
		"myapp@router-1.service.d/tt.conf",
		"myapp@storage-1.service.d/tt.conf",
		"myapp@worker.a.service.d/tt.conf",
	}, names)

	compareGolden(t, "units.golden", render(files))
}

// TestGenerateRefuses pins the names and the paths that a unit would have to
// escape or quote: they are refused rather than escaped.
func TestGenerateRefuses(t *testing.T) {
	cases := map[string]func(units *systemd.Units){
		"an application with a space": func(units *systemd.Units) { units.App = "my app" },
		"an application with a colon": func(units *systemd.Units) { units.App = "my:app" },
		"an application with an at":   func(units *systemd.Units) { units.App = "my@app" },
		"an application with a dash first": func(units *systemd.Units) {
			units.App = "-app"
		},
		"an empty application": func(units *systemd.Units) { units.App = "" },
		"an instance with a non-ASCII letter": func(units *systemd.Units) {
			units.Instances[0].Name = "stöd"
		},
		"an instance with a backslash": func(units *systemd.Units) {
			units.Instances[0].Name = `a\x2d`
		},
		"an instance with a percent": func(units *systemd.Units) {
			units.Instances[0].Name = "a%i"
		},
		"a long instance": func(units *systemd.Units) {
			units.Instances[0].Name = strings.Repeat("a", 250)
		},
		"a root with a space": func(units *systemd.Units) { units.Root = "/opt/my tarantool" },
		"a relative root":     func(units *systemd.Units) { units.Root = "opt/tarantool" },
		"tt with a dollar":    func(units *systemd.Units) { units.TT = "/opt/$tt/bin/tt" },
		"no user":             func(units *systemd.Units) { units.RunAs.User = "" },
		"a group with a space": func(units *systemd.Units) {
			units.RunAs.Group = "tt group"
		},
		"an unknown restart": func(units *systemd.Units) {
			units.Instances[0].Restart = "on-abort"
		},
	}

	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			units := sampleUnits()
			spoil(&units)

			files, err := systemd.Generate(units)
			require.Error(t, err)
			assert.Nil(t, files)
		})
	}

	units := sampleUnits()

	units.Instances[0].Name = "a:b@c_d.e-f"
	units.Instances[0].Restart = ""

	files, err := systemd.Generate(units)
	require.NoError(t, err, "what a unit name takes as it is")
	assert.Contains(t, string(files[1].Content), "Restart=no\n")
}

// TestNames pins the names of the units of an application.
func TestNames(t *testing.T) {
	assert.Equal(t, "myapp@.service", systemd.TemplateName("myapp"))
	assert.Equal(t, "myapp@storage-1.service", systemd.UnitName("myapp", "storage-1"))
	assert.Equal(t, "myapp@storage-1.service.d/tt.conf",
		systemd.DropInPath("myapp@storage-1.service"))
}

// TestParseRunAs pins --run-as: <user>[:<group>], the group of the user's
// name without one, and names a unit takes as they are.
func TestParseRunAs(t *testing.T) {
	for value, want := range map[string]systemd.RunAs{
		"tarantool":      {User: "tarantool", Group: "tarantool"},
		"tt-svc:daemon":  {User: "tt-svc", Group: "daemon"},
		"1001:1001":      {User: "1001", Group: "1001"},
		"svc.user_1:grp": {User: "svc.user_1", Group: "grp"},
	} {
		got, err := systemd.ParseRunAs(value)
		require.NoError(t, err, value)
		assert.Equal(t, want, got, value)
		assert.Equal(t, want.User+":"+want.Group, got.String(), value)
	}

	for _, value := range []string{
		"", ":", "svc:", ":grp", "-svc", "svc:-grp", "svc grp", "svc:grp:x", "%u", "svc$",
		"svc@host", `svc\x2d`,
	} {
		_, err := systemd.ParseRunAs(value)
		require.ErrorIs(t, err, systemd.ErrUnitAccount, value)
	}
}
