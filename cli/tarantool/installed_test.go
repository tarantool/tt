package tarantool_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/scope"
)

// installedScopes are the scopes whose applications run on the version their
// lock names.
var installedScopes = []binary.Scope{binary.ScopeUser, binary.ScopeSystem}

// scopeRoot points TT_USER_ROOT and TT_SYSTEM_ROOT at fresh directories and
// returns the root of appScope.
func scopeRoot(t *testing.T, appScope binary.Scope) string {
	t.Helper()

	roots := map[binary.Scope]string{
		binary.ScopeUser:   filepath.Join(t.TempDir(), "user root"),
		binary.ScopeSystem: filepath.Join(t.TempDir(), "system root"),
	}
	t.Setenv(scope.UserRootEnv, roots[binary.ScopeUser])
	t.Setenv(scope.SystemRootEnv, roots[binary.ScopeSystem])

	return roots[appScope]
}

// writeManifest writes a manifest requiring constraint of tarantool into dir.
func writeManifest(t *testing.T, dir, constraint string) {
	t.Helper()

	text := "manifest_version = \"0.1\"\n\n[package]\nname = \"app\"\n\n" +
		"[platform]\ntarantool = \"" + constraint + "\"\ntt = \">=3.0.0\"\n"

	require.NoError(t, os.MkdirAll(dir, dirMode))
	require.NoError(t, os.WriteFile(filepath.Join(dir, scope.ManifestFileName),
		[]byte(text), fileMode))
}

// installApp lays out an installed version of an application under root, with
// a manifest requiring constraint and a lock bundling bundled, and returns its
// directory.
func installApp(t *testing.T, root, constraint, bundled string) string {
	t.Helper()

	dir := filepath.Join(root, binary.VersionsDirName, "app", "app_1.0.0")
	writeManifest(t, dir, constraint)
	writeLock(t, dir, bundled)

	return dir
}

func TestResolveLockVersion(t *testing.T) {
	for _, appScope := range installedScopes {
		t.Run(appScope.String(), func(t *testing.T) {
			root := scopeRoot(t, appScope)
			pathTarantool(t)
			install(t, root, ce("3.7.0"))

			want := install(t, root, ce("3.8.1"))
			// The active link selects another version: tt binary switch must
			// not change the runtime of an installed application.
			activate(t, root, install(t, root, ce("3.9.0")))

			dir := installApp(t, root, ">=3.0.0,<4.0.0", "3.8.1")

			exe, err := tarantool.Resolve(tarantool.App{Scope: appScope, Dir: dir})
			require.NoError(t, err)
			assert.Equal(t, tarantool.Executable{
				Path:     want,
				Origin:   tarantool.OriginLock,
				Program:  binary.ProgramCe,
				Version:  "3.8.1",
				Warnings: nil,
			}, exe)
		})
	}
}

func TestResolveLockVersionNotInstalled(t *testing.T) {
	for _, appScope := range installedScopes {
		t.Run(appScope.String(), func(t *testing.T) {
			root := scopeRoot(t, appScope)
			pathTarantool(t)
			install(t, root, ce("3.8.0"))
			activate(t, root, install(t, root, ce("3.9.0")))

			// A version directory without an executable is an installation
			// that did not finish.
			incomplete := binary.InstallationPath(root, binary.ProgramCe, "3.8.1")
			require.NoError(t, os.MkdirAll(incomplete, dirMode))

			dir := installApp(t, root, ">=3.0.0,<4.0.0", "3.8.1")

			_, err := tarantool.Resolve(tarantool.App{Scope: appScope, Dir: dir})
			require.ErrorIs(t, err, tarantool.ErrNotInstalled)
			require.ErrorContains(t, err, "in the "+appScope.String()+" scope")
			require.ErrorContains(t, err, "bundles tarantool 3.8.1")
			assert.ErrorContains(t, err,
				"install it with: tt binary install tarantool 3.8.1 --scope "+appScope.String())
		})
	}
}

func TestResolveConstraint(t *testing.T) {
	for _, appScope := range installedScopes {
		t.Run(appScope.String(), func(t *testing.T) {
			root := scopeRoot(t, appScope)
			pathTarantool(t)

			for _, id := range []string{"2.11.1", "3.2.0", "3.10.0-entrypoint", "master",
				"1a2b3c4"} {
				install(t, root, ce(id))
			}

			want := install(t, root, ce("3.10.2"))
			install(t, root, ce("3.8.1"))
			activate(t, root, install(t, root, ce("4.0.0")))
			// Not installed: there is no executable.
			require.NoError(t, os.MkdirAll(
				binary.InstallationPath(root, binary.ProgramCe, "3.11.0"), dirMode))

			// An archive packed --without-deps bundles no tarantool.
			dir := installApp(t, root, ">=3.0.0,<4.0.0", "")

			exe, err := tarantool.Resolve(tarantool.App{Scope: appScope, Dir: dir})
			require.NoError(t, err)
			assert.Equal(t, tarantool.Executable{
				Path:     want,
				Origin:   tarantool.OriginConstraint,
				Program:  binary.ProgramCe,
				Version:  "3.10.2",
				Warnings: nil,
			}, exe)
		})
	}
}

func TestResolveConstraintPrerelease(t *testing.T) {
	root := scopeRoot(t, binary.ScopeUser)
	install(t, root, ce("3.8.1"))

	want := install(t, root, ce("3.9.0-rc1"))
	dir := installApp(t, root, ">=3.0.0,<4.0.0", "")

	// A prerelease satisfies the constraint by its core version, as it does
	// for tt package pack, and is reported.
	exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeUser, Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, want, exe.Path)
	require.Len(t, exe.Warnings, 1)
	assert.Contains(t, exe.Warnings[0], "tarantool 3.9.0-rc1")
	assert.Contains(t, exe.Warnings[0], "prerelease")

	// The release is above its prereleases.
	want = install(t, root, ce("3.9.0"))

	exe, err = tarantool.Resolve(tarantool.App{Scope: binary.ScopeUser, Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, want, exe.Path)
	assert.Empty(t, exe.Warnings)
}

func TestResolveConstraintNothingFits(t *testing.T) {
	for _, appScope := range installedScopes {
		t.Run(appScope.String(), func(t *testing.T) {
			root := scopeRoot(t, appScope)
			pathTarantool(t)
			install(t, root, ce("2.11.1"))
			activate(t, root, install(t, root, ce("4.0.0")))

			dir := installApp(t, root, ">=3.0.0,<4.0.0", "")

			_, err := tarantool.Resolve(tarantool.App{Scope: appScope, Dir: dir})
			require.ErrorIs(t, err, tarantool.ErrNotInstalled)
			require.ErrorContains(t, err, `">=3.0.0,<4.0.0"`)
			assert.ErrorContains(t, err,
				"tt binary install tarantool <version> --scope "+appScope.String())
		})
	}
}

func TestResolveConstraintNothingInstalled(t *testing.T) {
	root := scopeRoot(t, binary.ScopeSystem)
	dir := installApp(t, root, ">=3.0.0,<4.0.0", "")

	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeSystem, Dir: dir})
	require.ErrorIs(t, err, tarantool.ErrNotInstalled)
}

func TestResolveConstraintInvalid(t *testing.T) {
	root := scopeRoot(t, binary.ScopeUser)
	install(t, root, ce("3.8.1"))

	dir := installApp(t, root, "three or so", "")

	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeUser, Dir: dir})
	require.ErrorContains(t, err, `invalid version constraint "three or so"`)
}

func TestResolveFlavor(t *testing.T) {
	cases := []struct {
		name       string
		constraint string
		bundled    string
		installed  []installation
		want       installation
		wantErr    string
	}{
		{
			name:       "ce lock prefers ce",
			constraint: ">=3.0.0,<4.0.0", bundled: "3.8.1",
			installed: []installation{ee("3.8.1"), ce("3.8.1")},
			want:      ce("3.8.1"),
		},
		{
			name:       "ce lock runs on ee",
			constraint: ">=3.0.0,<4.0.0[ce]", bundled: "3.8.1",
			installed: []installation{ee("3.8.1"), ce("3.9.0")},
			want:      ee("3.8.1"),
		},
		{
			name:       "ee lock",
			constraint: ">=3.0.0,<4.0.0[ee]", bundled: "3.8.1",
			installed: []installation{ce("3.8.1"), ee("3.8.1")},
			want:      ee("3.8.1"),
		},
		{
			name:       "ee lock does not run on ce",
			constraint: ">=3.0.0,<4.0.0[ee]", bundled: "3.8.1",
			installed: []installation{ce("3.8.1")},
			wantErr:   "install it with: tt binary install tarantool 3.8.1ee --scope user",
		},
		{
			name:       "ee archive version as standalone",
			constraint: ">=3.0.0,<4.0.0[ee]", bundled: "gc64-3.8.1-0-r93",
			installed: []installation{ee("3.8.0"), ee("3.8.1")},
			want:      ee("3.8.1"),
		},
		{
			name:       "ee archive version as sdk component",
			constraint: ">=3.0.0,<4.0.0[ee]", bundled: "gc64-3.8.1-0-r93",
			installed: []installation{ee("3.8.1"), ee("sdk-gc64-3.8.1-0-r93")},
			want:      ee("sdk-gc64-3.8.1-0-r93"),
		},
		{
			name:       "ee archive version not installed",
			constraint: ">=3.0.0,<4.0.0[ee]", bundled: "gc64-3.8.1-0-r93",
			installed: []installation{ee("sdk-gc64-3.8.1-0-r94"), ee("3.8.0")},
			wantErr: "install it with: " +
				"tt binary install tarantool gc64-3.8.1-0-r93 --scope user",
		},
		{
			name:       "ce constraint prefers ce",
			constraint: ">=3.0.0,<4.0.0",
			installed:  []installation{ee("3.8.1"), ce("3.2.0")},
			want:       ce("3.2.0"),
		},
		{
			name:       "ce constraint runs on ee",
			constraint: ">=3.0.0,<4.0.0[ce]",
			installed:  []installation{ee("3.8.1"), ee("3.2.0"), ce("4.0.0")},
			want:       ee("3.8.1"),
		},
		{
			name:       "ee constraint",
			constraint: ">=3.0.0,<4.0.0[ee]",
			installed: []installation{
				ce("3.9.0"), ee("gc64-2.11.1"), ee("3.2.0"), ee("sdk-gc64-3.8.1-0-r93"),
			},
			want: ee("sdk-gc64-3.8.1-0-r93"),
		},
		{
			name:       "ee constraint between installations of one version",
			constraint: ">=3.0.0,<4.0.0[ee]",
			installed:  []installation{ee("debug-3.8.1"), ee("3.8.1")},
			want:       ee("3.8.1"),
		},
		{
			name:       "ee constraint does not run on ce",
			constraint: ">=3.0.0,<4.0.0[ee]",
			installed:  []installation{ce("3.8.1")},
			wantErr:    "tt binary install tarantool <version>ee --scope user",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := scopeRoot(t, binary.ScopeUser)
			for _, inst := range testCase.installed {
				install(t, root, inst)
			}

			dir := installApp(t, root, testCase.constraint, testCase.bundled)

			exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeUser, Dir: dir})
			if testCase.wantErr != "" {
				require.ErrorIs(t, err, tarantool.ErrNotInstalled)
				assert.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want.program, exe.Program)
			assert.Equal(t, testCase.want.id, exe.Version)
			assert.Equal(t, filepath.Join(
				binary.InstallationPath(root, testCase.want.program, testCase.want.id),
				"tarantool"), exe.Path)
		})
	}
}

func TestResolveInstalledIgnoresUseSystem(t *testing.T) {
	for _, appScope := range installedScopes {
		t.Run(appScope.String(), func(t *testing.T) {
			root := scopeRoot(t, appScope)
			pathTarantool(t)
			t.Setenv(tarantool.UseSystemEnv, "1")

			fromLock := install(t, root, ce("3.8.1"))
			fromConstraint := install(t, root, ce("3.9.0"))

			exe, err := tarantool.Resolve(tarantool.App{
				Scope: appScope, Dir: installApp(t, root, ">=3.0.0,<4.0.0", "3.8.1"),
			})
			require.NoError(t, err)
			assert.Equal(t, fromLock, exe.Path)
			assert.Equal(t, tarantool.OriginLock, exe.Origin)

			exe, err = tarantool.Resolve(tarantool.App{
				Scope: appScope, Dir: installApp(t, root, ">=3.0.0,<4.0.0", ""),
			})
			require.NoError(t, err)
			assert.Equal(t, fromConstraint, exe.Path)
			assert.Equal(t, tarantool.OriginConstraint, exe.Origin)
		})
	}
}

func TestResolveInstalledBrokenApplication(t *testing.T) {
	root := scopeRoot(t, binary.ScopeUser)
	install(t, root, ce("3.8.1"))

	dir := filepath.Join(root, binary.VersionsDirName, "app", "app_1.0.0")
	app := tarantool.App{Scope: binary.ScopeUser, Dir: dir}

	writeLock(t, dir, "3.8.1")

	_, err := tarantool.Resolve(app)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "cannot read application manifest")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"),
		[]byte("manifest_version = "), fileMode))

	_, err = tarantool.Resolve(app)
	require.ErrorContains(t, err, "cannot parse "+filepath.Join(dir, "app.manifest.toml"))

	// Every archive carries its lock, so an installed version without one is
	// not taken for an archive packed --without-deps.
	writeManifest(t, dir, ">=3.0.0,<4.0.0")
	require.NoError(t, os.Remove(filepath.Join(dir, "app.manifest.lock")))

	_, err = tarantool.Resolve(app)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, filepath.Join(dir, "app.manifest.lock"))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.lock"),
		[]byte("lock_version = "), fileMode))

	_, err = tarantool.Resolve(app)
	require.ErrorContains(t, err, "cannot parse "+filepath.Join(dir, "app.manifest.lock"))
}

func TestResolveInstalledBadRoot(t *testing.T) {
	t.Setenv(scope.UserRootEnv, "relative/root")

	_, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeUser, Dir: t.TempDir()})
	require.ErrorIs(t, err, scope.ErrRelativeRoot)
}
