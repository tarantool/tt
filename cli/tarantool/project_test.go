package tarantool_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/scope"
)

// emptyPath makes PATH a directory holding nothing.
func emptyPath(t *testing.T) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())
}

// project returns a project directory with tarantool installed into its
// .tarantool and the active link selecting the installation, and the
// executable the link selects.
func project(t *testing.T, inst installation) (string, string) {
	t.Helper()

	dir := t.TempDir()
	root := scope.ProjectRoot(dir)
	target := install(t, root, inst)
	activate(t, root, target)

	return dir, target
}

func TestResolveProjectLink(t *testing.T) {
	for _, inst := range []installation{ce("3.8.1"), ee("3.8.1"), ee("sdk-gc64-3.8.1-0-r93")} {
		t.Run(inst.program.String()+" "+inst.id, func(t *testing.T) {
			pathTarantool(t)

			dir, target := project(t, inst)
			install(t, scope.ProjectRoot(dir), ce("3.9.0"))

			exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
			require.NoError(t, err)
			assert.Equal(t, tarantool.Executable{
				Path:     target,
				Origin:   tarantool.OriginProject,
				Program:  inst.program,
				Version:  inst.id,
				Warnings: nil,
			}, exe)
		})
	}
}

func TestResolveProjectRelativeLink(t *testing.T) {
	emptyPath(t)

	dir := t.TempDir()
	root := scope.ProjectRoot(dir)
	target := install(t, root, ce("3.8.1"))
	activate(t, root, filepath.Join("..", "versions", "tarantool", "tarantool_3.8.1",
		"tarantool"))

	exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, target, exe.Path)
	assert.Equal(t, "3.8.1", exe.Version)
}

func TestResolveProjectOutsideInstallations(t *testing.T) {
	emptyPath(t)

	// The active link may select a build tt binary install --from-build
	// linked, or be an executable put there by hand: either runs, with its
	// version unknown, so the lock is not compared with it.
	build := filepath.Join(t.TempDir(), "build", "src", "tarantool")
	writeExecutable(t, build)

	linked := t.TempDir()
	activate(t, scope.ProjectRoot(linked), build)

	copied := t.TempDir()
	copiedPath := filepath.Join(scope.ProjectRoot(copied), "bin", "tarantool")
	writeExecutable(t, copiedPath)

	for dir, want := range map[string]string{linked: build, copied: copiedPath} {
		writeLock(t, dir, "3.8.1")

		exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, tarantool.Executable{
			Path:     want,
			Origin:   tarantool.OriginProject,
			Program:  binary.ProgramUnknown,
			Version:  "",
			Warnings: nil,
		}, exe)
	}
}

func TestResolveProjectBrokenLink(t *testing.T) {
	pathTarantool(t)

	dir, target := project(t, ce("3.8.1"))
	require.NoError(t, os.Remove(target))

	// The project chose a tarantool; another one from PATH is not run in its
	// place.
	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
	require.ErrorContains(t, err, "the project tarantool is not an executable")
	require.ErrorContains(t, err, target)
	require.ErrorContains(t, err, "tt binary switch tarantool")

	link := filepath.Join(scope.ProjectRoot(t.TempDir()), "bin", "tarantool")
	require.NoError(t, os.MkdirAll(link, dirMode))

	_, err = tarantool.Resolve(tarantool.App{
		Scope: binary.ScopeProject, Dir: filepath.Dir(filepath.Dir(filepath.Dir(link))),
	})
	require.ErrorContains(t, err, "the project tarantool is not an executable")
}

func TestResolveProjectPath(t *testing.T) {
	want := pathTarantool(t)
	dir := t.TempDir()
	// A version the lock bundles is not compared with a tarantool from PATH:
	// its version is only known by running it.
	writeLock(t, dir, "3.8.1")

	exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, tarantool.Executable{
		Path:     want,
		Origin:   tarantool.OriginPath,
		Program:  binary.ProgramUnknown,
		Version:  "",
		Warnings: nil,
	}, exe)
}

func TestResolveProjectPathWithoutActiveTarantool(t *testing.T) {
	want := pathTarantool(t)
	dir := t.TempDir()
	// The project has a .tarantool, but no active tarantool in it: bin is not
	// even a directory.
	install(t, scope.ProjectRoot(dir), installation{program: binary.ProgramTt, id: "3.0.0"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tarantool", "bin"), nil, fileMode))

	exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, want, exe.Path)
	assert.Equal(t, tarantool.OriginPath, exe.Origin)
}

func TestResolveProjectNoTarantool(t *testing.T) {
	emptyPath(t)

	dir := t.TempDir()

	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
	require.ErrorIs(t, err, tarantool.ErrNoTarantool)
	require.ErrorContains(t, err, filepath.Join(dir, ".tarantool", "bin", "tarantool"))
	assert.ErrorContains(t, err, "tt binary install tarantool")
}

func TestResolveProjectLockMismatch(t *testing.T) {
	cases := []struct {
		name    string
		active  installation
		lock    string // Lock contents; empty means no lock.
		bundled string
		warning string // Empty means no warning.
	}{
		{
			name:   "matching",
			active: ce("3.8.1"), bundled: "3.8.1",
		},
		{
			name:   "different version",
			active: ce("3.8.1"), bundled: "3.9.0",
			warning: "the project runs tarantool 3.8.1 (%s), " +
				"but app.manifest.lock bundles 3.9.0",
		},
		{
			name:   "no lock",
			active: ce("3.8.1"),
		},
		{
			name:   "lock without a bundled version",
			active: ce("3.8.1"), lock: "write", bundled: "",
		},
		{
			name:   "enterprise archive version",
			active: ee("3.8.1"), bundled: "gc64-3.8.1-0-r93",
		},
		{
			name:   "enterprise archive version on community",
			active: ce("3.8.1"), bundled: "gc64-3.8.1-0-r93",
			warning: "the project runs tarantool 3.8.1 (%s), " +
				"but app.manifest.lock bundles gc64-3.8.1-0-r93",
		},
		{
			name:   "enterprise sdk component",
			active: ee("sdk-gc64-3.8.1-0-r93"), bundled: "gc64-3.8.1-0-r93",
		},
		{
			name:   "unreadable lock",
			active: ce("3.8.1"), lock: "lock_version = ",
			warning: "cannot compare the project tarantool with app.manifest.lock: " +
				"cannot parse ",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			emptyPath(t)

			dir, target := project(t, testCase.active)

			switch {
			case testCase.bundled != "" || testCase.lock == "write":
				writeLock(t, dir, testCase.bundled)
			case testCase.lock != "":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.lock"),
					[]byte(testCase.lock), fileMode))
			}

			exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
			require.NoError(t, err)
			assert.Equal(t, target, exe.Path)

			if testCase.warning == "" {
				assert.Empty(t, exe.Warnings)

				return
			}

			require.Len(t, exe.Warnings, 1)

			want := testCase.warning
			if testCase.lock == "" {
				want = fmt.Sprintf(want, target)
			}

			assert.Contains(t, exe.Warnings[0], want)
		})
	}
}

func TestResolveProjectUseSystem(t *testing.T) {
	for value, fromPath := range map[string]bool{
		"1": true, "yes": true, "TRUE": true,
		"": false, "0": false, "false": false, "False": false,
	} {
		t.Run(value, func(t *testing.T) {
			onPath := pathTarantool(t)
			dir, target := project(t, ce("3.8.1"))
			writeLock(t, dir, "3.9.0")
			t.Setenv(tarantool.UseSystemEnv, value)

			exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
			require.NoError(t, err)

			if fromPath {
				assert.Equal(t, onPath, exe.Path)
				assert.Equal(t, tarantool.OriginPath, exe.Origin)
				assert.Empty(t, exe.Warnings)
			} else {
				assert.Equal(t, target, exe.Path)
				assert.Equal(t, tarantool.OriginProject, exe.Origin)
				assert.Len(t, exe.Warnings, 1)
			}
		})
	}
}

func TestResolveProjectUseSystemWithoutPath(t *testing.T) {
	emptyPath(t)

	dir, _ := project(t, ce("3.8.1"))
	t.Setenv(tarantool.UseSystemEnv, "1")

	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: dir})
	require.ErrorIs(t, err, tarantool.ErrNoTarantool)
	assert.ErrorContains(t, err, tarantool.UseSystemEnv)
}

func TestResolveRelativeDir(t *testing.T) {
	emptyPath(t)

	dir, target := project(t, ce("3.8.1"))
	t.Chdir(filepath.Dir(dir))

	exe, err := tarantool.Resolve(tarantool.App{
		Scope: binary.ScopeProject, Dir: filepath.Base(dir),
	})
	require.NoError(t, err)
	assert.Equal(t, target, exe.Path)
}
