package systemd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/systemd"
)

// sampleMark is the mark of sampleUnits installed into unitDir.
func sampleMark(unitDir string) systemd.Mark {
	return systemd.Mark{
		UnitDir:  unitDir,
		Template: "myapp@.service",
		RunAs:    "tt-svc:tarantool",
		Instances: map[string]string{
			"router-1":  "myapp@router-1.service",
			"storage-1": "myapp@storage-1.service",
			"worker.a":  "myapp@worker.a.service",
		},
	}
}

// installSample installs sampleUnits into a new unit directory and returns
// the unit directory and the host directory.
func installSample(t *testing.T) (string, string) {
	t.Helper()

	unitDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "etc", "myapp")

	files, err := systemd.Generate(sampleUnits())
	require.NoError(t, err)

	require.NoError(t, systemd.Install(systemd.Installation{
		UnitDir: unitDir,
		HostDir: hostDir,
		Files:   files,
		Mark:    sampleMark(unitDir),
		Stale:   nil,
	}))

	return unitDir, hostDir
}

// listFiles returns the files under dir, relative to it.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()

	var files []string

	require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dir, path)

		files = append(files, rel)

		return err
	}))

	return files
}

// TestInstallUninstall pins that install writes the units and the mark, and
// uninstall removes both, with the drop-in directories.
func TestInstallUninstall(t *testing.T) {
	unitDir, hostDir := installSample(t)

	assert.Equal(t, []string{
		"myapp@.service",
		"myapp@router-1.service.d/tt.conf",
		"myapp@storage-1.service.d/tt.conf",
		"myapp@worker.a.service.d/tt.conf",
	}, listFiles(t, unitDir))

	info, err := os.Stat(filepath.Join(unitDir, "myapp@.service"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	mark, found, err := systemd.ReadMark(hostDir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, sampleMark(unitDir), mark)

	data, err := os.ReadFile(systemd.MarkPath(hostDir))
	require.NoError(t, err)
	assert.Contains(t, string(data), "# Written by tt service systemd install")
	assert.Contains(t, string(data), "  storage-1: myapp@storage-1.service\n")
	assert.Contains(t, string(data), "\nrun_as: tt-svc:tarantool\n")

	kept, err := systemd.Uninstall(hostDir, mark)
	require.NoError(t, err)
	assert.Empty(t, kept)

	entries, err := os.ReadDir(unitDir)
	require.NoError(t, err)
	assert.Empty(t, entries)

	_, found, err = systemd.ReadMark(hostDir)
	require.NoError(t, err)
	assert.False(t, found)

	// What is gone already is no failure.
	kept, err = systemd.Uninstall(hostDir, mark)
	require.NoError(t, err)
	assert.Empty(t, kept)
}

// TestInstallAgain pins that an install over the units installed before
// replaces them and removes the stale drop-ins.
func TestInstallAgain(t *testing.T) {
	unitDir, hostDir := installSample(t)

	units := sampleUnits()

	units.Instances = units.Instances[1:]

	files, err := systemd.Generate(units)
	require.NoError(t, err)

	mark := sampleMark(unitDir)
	delete(mark.Instances, "router-1")

	require.NoError(t, systemd.Install(systemd.Installation{
		UnitDir: unitDir,
		HostDir: hostDir,
		Files:   files,
		Mark:    mark,
		Stale:   []string{"myapp@router-1.service.d/tt.conf"},
	}))

	assert.Equal(t, []string{
		"myapp@.service",
		"myapp@storage-1.service.d/tt.conf",
		"myapp@worker.a.service.d/tt.conf",
	}, listFiles(t, unitDir))

	got, _, err := systemd.ReadMark(hostDir)
	require.NoError(t, err)
	assert.Equal(t, mark, got)
}

// TestInstallRefusesForeign pins that install replaces only the files tt
// wrote, and writes nothing, the mark included, when a file in the place of
// a unit is someone else's.
func TestInstallRefusesForeign(t *testing.T) {
	unitDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "myapp")

	foreign := filepath.Join(unitDir, "myapp@storage-1.service.d", "tt.conf")
	require.NoError(t, os.MkdirAll(filepath.Dir(foreign), 0o755))
	require.NoError(t, os.WriteFile(foreign, []byte("[Service]\nRestart=always\n"), 0o644))

	files, err := systemd.Generate(sampleUnits())
	require.NoError(t, err)

	err = systemd.Install(systemd.Installation{
		UnitDir: unitDir, HostDir: hostDir, Files: files, Mark: sampleMark(unitDir),
	})
	require.ErrorIs(t, err, systemd.ErrForeignFile)
	require.ErrorContains(t, err, foreign)

	assert.Equal(t, []string{"myapp@storage-1.service.d/tt.conf"}, listFiles(t, unitDir))
	assert.NoDirExists(t, hostDir)

	err = systemd.WriteFiles(unitDir, files)
	require.ErrorIs(t, err, systemd.ErrForeignFile)
}

// TestInstallNeedsUnitDir pins that install does not create the unit
// directory, the directory of the service manager, and writes no mark
// without it.
func TestInstallNeedsUnitDir(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "no-such")
	hostDir := filepath.Join(t.TempDir(), "myapp")

	files, err := systemd.Generate(sampleUnits())
	require.NoError(t, err)

	err = systemd.Install(systemd.Installation{
		UnitDir: unitDir, HostDir: hostDir, Files: files, Mark: sampleMark(unitDir),
	})
	require.ErrorIs(t, err, systemd.ErrNoUnitDir)
	assert.NoDirExists(t, unitDir)
	assert.NoDirExists(t, hostDir)

	require.NoError(t, systemd.WriteFiles(unitDir, files), "generate creates its directory")
	assert.Len(t, listFiles(t, unitDir), 4)
}

// TestInstallUnwritableUnitDir pins that a unit directory tt cannot write to
// fails the install before the mark is written.
func TestInstallUnwritableUnitDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into any directory")
	}

	unitDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "myapp")

	require.NoError(t, os.Chmod(unitDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(unitDir, 0o755) })

	files, err := systemd.Generate(sampleUnits())
	require.NoError(t, err)

	err = systemd.Install(systemd.Installation{
		UnitDir: unitDir, HostDir: hostDir, Files: files, Mark: sampleMark(unitDir),
	})
	require.ErrorIs(t, err, os.ErrPermission)
	assert.NoDirExists(t, hostDir)
	assert.Empty(t, listFiles(t, unitDir))
}

// TestUninstallKeepsForeign pins that uninstall leaves a file tt did not
// write, and the drop-in directory it is in, and reports it.
func TestUninstallKeepsForeign(t *testing.T) {
	unitDir, hostDir := installSample(t)

	edited := filepath.Join(unitDir, "myapp@router-1.service.d", "tt.conf")
	require.NoError(t, os.WriteFile(edited, []byte("[Service]\nRestart=always\n"), 0o644))

	own := filepath.Join(unitDir, "myapp@storage-1.service.d", "override.conf")
	require.NoError(t, os.WriteFile(own, []byte("[Service]\nNice=5\n"), 0o644))

	mark, _, err := systemd.ReadMark(hostDir)
	require.NoError(t, err)

	kept, err := systemd.Uninstall(hostDir, mark)
	require.NoError(t, err)
	assert.Equal(t, []string{edited}, kept)

	assert.Equal(t, []string{
		"myapp@router-1.service.d/tt.conf",
		"myapp@storage-1.service.d/override.conf",
	}, listFiles(t, unitDir))
}

// TestReadMarkInvalid pins that a mark that does not say what the units
// manage is an error, and no mark is none.
func TestReadMarkInvalid(t *testing.T) {
	hostDir := t.TempDir()

	mark, found, err := systemd.ReadMark(hostDir)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, mark.Instances)

	for name, content := range map[string]string{
		"not yaml":      "unit_dir: [\n",
		"unknown field": "unit_dir: /etc/systemd/system\ntemplate: a@.service\nunits: {}\n",
		"relative dir":  "unit_dir: etc\ntemplate: a@.service\n",
		"no template":   "unit_dir: /etc/systemd/system\n",
		"no unit":       "unit_dir: /etc/systemd/system\ntemplate: a@.service\ninstances: {a: }\n",
		"empty instance": "unit_dir: /etc/systemd/system\ntemplate: a@.service\n" +
			"instances: {'': b}\n",
		"bad run_as": "unit_dir: /etc/systemd/system\ntemplate: a@.service\n" +
			"run_as: 'tt svc'\n",
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(systemd.MarkPath(hostDir), []byte(content), 0o644))

			_, _, err := systemd.ReadMark(hostDir)
			require.ErrorIs(t, err, systemd.ErrInvalidMark)
		})
	}
}
