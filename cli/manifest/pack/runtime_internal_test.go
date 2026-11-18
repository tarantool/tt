package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/binary"
)

func constraint(spec string) manifest.Constraint {
	return manifest.Constraint{Version: spec}
}

// fakeCache builds a runtime cache with the given component versions, all under
// the ce flavor. Every tarantool entry gets a LICENSE, the shape of a CE
// install; an entry without one is packed with a warning instead.
func fakeCache(t *testing.T, entries map[string][]string) string {
	t.Helper()

	return fakeFlavorCache(t, flavorCE, entries)
}

// fakeFlavorCache builds a runtime cache under one explicit flavor.
func fakeFlavorCache(t *testing.T, flavor string, entries map[string][]string) string {
	t.Helper()

	cache := t.TempDir()

	for component, versions := range entries {
		for _, ver := range versions {
			dir := filepath.Join(cache, component, flavor, ver)
			files := map[string]string{
				filepath.Join("bin", component): "#!/bin/sh\n",
			}

			if component == runtimeTarantool {
				files["LICENSE"] = "BSD-2-Clause"
			}

			writeTree(t, dir, files)
		}
	}

	return cache
}

// TestSatisfies uses the manifest's documented constraint syntax - a
// comma-separated hashicorp range, as in testdata/my-app.toml's
// ">=3.0.0,<4.0.0[ce]". Caret ranges are npm/cargo syntax and are rejected.
func TestSatisfies(t *testing.T) {
	tests := []struct {
		name string
		ver  string
		spec string
		want bool
	}{
		{"range matches", "3.2.0", ">=3.0.0,<4.0.0", true},
		{"range rejects major bump", "4.0.0", ">=3.0.0,<4.0.0", false},
		{"range rejects below floor", "2.11.0", ">=3.0.0,<4.0.0", false},
		{"lower bound only", "3.1.0", ">=3.1.0", true},
		{"empty constraint accepts anything", "3.0.5", "", true},
		{"empty version never satisfies", "", ">=3.0.0", false},
		{"leading v is tolerated", "v3.0.5", ">=3.0.0,<4.0.0", true},
		{"exact pin", "3.0.5", "3.0.5", true},
		{"unparseable version is a miss", "not-a-version", ">=3.0.0", false},
		// Every Tarantool development build reports a prerelease suffix; under
		// strict semver it would satisfy no ordinary range at all.
		{"tarantool entrypoint build", "3.8.0-entrypoint-49-g97a3b38040",
			">=3.0.0,<4.0.0", true},
		{"prerelease still respects bounds", "4.0.0-entrypoint-1-gabc",
			">=3.0.0,<4.0.0", false},
		{"build metadata is dropped", "3.2.0+deadbeef", ">=3.0.0,<4.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := satisfies(tt.ver, constraint(tt.spec))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSatisfiesRejectsBadConstraint pins the no-silent-fallback rule: a typo in
// [platform] must fail loudly rather than degrade to an exact-string match and
// bundle the wrong runtime.
func TestSatisfiesRejectsBadConstraint(t *testing.T) {
	for _, spec := range []string{"^3.0", "~3.0.0.0", "not a constraint"} {
		_, err := satisfies("3.0.5", constraint(spec))
		require.Error(t, err, "constraint %q must be rejected", spec)
		assert.ErrorIs(t, err, errBadConstraint)
	}
}

// TestFindInCachePicksHighest is what makes the cache deterministic: with
// several satisfying versions present, the highest wins, not the first read.
func TestFindInCachePicksHighest(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.0.1", "3.2.0", "3.10.0", "2.11.0", "4.0.0"},
	})

	dir, ver, ok, err := findInCache(
		cache, runtimeTarantool, flavorCE, constraint(">=3.0.0,<4.0.0"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "3.10.0", ver, "3.10.0 > 3.2.0 numerically, not lexically")
	assert.Equal(t, filepath.Join(cache, runtimeTarantool, flavorCE, "3.10.0"), dir)
}

func TestFindInCacheMisses(t *testing.T) {
	cache := fakeCache(t, map[string][]string{runtimeTarantool: {"2.11.0"}})

	_, _, ok, err := findInCache(cache, runtimeTarantool, flavorCE, constraint(">=3.0.0,<4.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "no satisfying version")

	_, _, ok, err = findInCache(cache, runtimeTt, flavorCE, constraint(">=2.0.0,<3.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "component absent from cache")

	_, _, ok, err = findInCache("", runtimeTarantool, flavorCE, constraint(">=3.0.0,<4.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "no cache configured")
}

func TestBundleRuntimeFromCache(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.0.5", "3.1.0"},
		runtimeTt:        {"2.5.0"},
	})
	stageDir := t.TempDir()

	var warnings []string

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		Warn: func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, "3.1.0", bundled.Tarantool)
	assert.Equal(t, "2.5.0", bundled.Tt)
	assert.Empty(t, bundled.Tcm, "tcm unset in [platform] is not bundled")
	assert.Empty(t, warnings, "the cache path must not warn")

	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "bin", "tarantool"))
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "LICENSE"))
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tt", "bin", "tt"))
	assert.NoDirExists(t, filepath.Join(stageDir, "_runtime", "tcm"))
}

func TestBundleRuntimeIncludesTcmWhenDeclared(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.0.5"},
		runtimeTt:        {"2.5.0"},
		runtimeTcm:       {"1.5.2"},
	})
	stageDir := t.TempDir()

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
			Tcm:       constraint(">=1.5.0,<2.0.0"),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "1.5.2", bundled.Tcm)
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tcm", "bin", "tcm"))
}

// TestBundleRuntimeFallsBackToActive covers the v0 convenience path: with no
// runtime cache yet, the active binary is accepted when it satisfies the
// constraint — and the user is told the archive is not cache-reproducible.
func TestBundleRuntimeFallsBackToActive(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "BSD-2-Clause",
		"bin/tt":        "#!/bin/sh\n",
	})

	stageDir := t.TempDir()

	var warnings []string

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "3.0.5",
		ActiveTt:               filepath.Join(prefix, "bin", "tt"),
		ActiveTtVersion:        "2.4.0",
		Warn:                   func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, "3.0.5", bundled.Tarantool)
	assert.Equal(t, "2.4.0", bundled.Tt)
	assert.Len(t, warnings, 2, "each fallback must warn")

	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "bin", "tarantool"))
	// The license is picked up from beside the binary's install prefix.
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "LICENSE"))
}

// TestBundleRuntimeRejectsEmptyPlatform is a regression test. The [platform]
// constraints reach bundleRuntime from the built manifest; when nothing filled
// them, every component was skipped as "unset" and a with-deps pack produced an
// archive with no _runtime/ at all - silently, and byte-comparable to
// --without-deps. An empty platform must fail loudly instead.
func TestBundleRuntimeRejectsEmptyPlatform(t *testing.T) {
	tests := []struct {
		name     string
		platform manifest.Platform
	}{
		{"nothing set", manifest.Platform{}},
		{"tarantool only", manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
		}},
		{"tt only", manifest.Platform{Tt: constraint(">=2.0.0,<3.0.0")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stageDir := t.TempDir()

			_, err := bundleRuntime(stageDir, RuntimeOptions{
				CacheDir: fakeCache(t, map[string][]string{
					runtimeTarantool: {"3.0.5"},
					runtimeTt:        {"2.5.0"},
				}),
				Platform: tt.platform,
			})

			require.Error(t, err)
			require.ErrorIs(t, err, errNoRuntime)
			assert.NoDirExists(t, filepath.Join(stageDir, "_runtime"))
		})
	}
}

// TestBundleRuntimeRejectsUnsatisfiedActive guards against silently bundling
// the wrong Tarantool: a fallback that misses the constraint is an error, not
// a warning.
func TestBundleRuntimeRejectsUnsatisfiedActive(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{"bin/tarantool": "#!/bin/sh\n"})

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "2.11.0",
	})

	require.Error(t, err)
	require.ErrorIs(t, err, errNoRuntime)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

// TestBundleRuntimeNoSourceAtAll: with no match in the runtime cache and no
// active binary, a tt with no tarantool say, the pack fails saying that none
// was found, not that one does not satisfy the constraint. With the cache
// holding one, no active binary is needed (TestBundleRuntimeFromCache).
func TestBundleRuntimeNoSourceAtAll(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "empty")

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
	})

	require.Error(t, err)
	require.ErrorIs(t, err, errNoRuntime)
	assert.Contains(t, err.Error(), "no tarantool satisfying >=3.0.0,<4.0.0 found in the "+
		"runtime cache "+cache+" or the project, and tt found no tarantool to bundle instead")
	assert.Contains(t, err.Error(), "install one with tt binary install tarantool")
	assert.NotContains(t, err.Error(), "does not satisfy")
}

// TestInstallCommand: the hints of pack name commands tt binary install has.
// An Enterprise product is installed through the command of its Community
// one, with the ee suffix on the version: tt binary install tarantool-ee
// and tt binary install tt-ee are no commands.
func TestInstallCommand(t *testing.T) {
	t.Parallel()

	for _, example := range []struct{ name, flavor, want string }{
		{runtimeTarantool, flavorCE, "tt binary install tarantool"},
		{runtimeTarantool, flavorEE, "tt binary install tarantool <version>ee"},
		{runtimeTt, flavorCE, "tt binary install tt"},
		{runtimeTt, flavorEE, "tt binary install tt <version>ee"},
		{runtimeTcm, flavorCE, "tt binary install tcm"},
		{runtimeTcm, flavorEE, "tt binary install tcm"},
	} {
		assert.Equal(t, example.want, installCommand(example.name, example.flavor), example)
	}

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: manifest.Constraint{Version: ">=3.0.0", Flavor: flavorEE},
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
	})

	require.ErrorIs(t, err, errNoRuntime)
	assert.Contains(t, err.Error(), "install one with tt binary install tarantool <version>ee,")
	assert.NotContains(t, err.Error(), "tarantool-ee")
}

// fakeInstallation lays out a tt binary installation of program at version
// in the project scope rooted at root: the executable at the top and, for
// tarantool, its headers.
func fakeInstallation(t *testing.T, root string, program binary.Program, ver string) {
	t.Helper()

	files := map[string]string{program.Exec(): "#!/bin/sh\n"}

	if program.IsTarantool() {
		files["include/tarantool/module.h"] = "/* " + ver + " */\n"
	}

	writeTree(t, binary.InstallationPath(root, program, ver), files)
}

// activate points the active link of the scope at the installation, as
// tt binary install and tt binary switch do.
func activate(t *testing.T, root string, program binary.Program, ver string) {
	t.Helper()

	link := filepath.Join(root, binary.BinDirName, program.Exec())
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(
		filepath.Join(binary.InstallationPath(root, program, ver), program.Exec()), link))
}

// TestBundleRuntimeFromProjectInstallations: with nothing in the cache, the
// runtimes tt binary installed into the project are bundled, without the
// warning of the fallback, and the active one wins over a higher one. The
// headers of the tarantool come along, and the lock records the name of the
// installation.
func TestBundleRuntimeFromProjectInstallations(t *testing.T) {
	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramCe, "3.8.1")
	fakeInstallation(t, root, binary.ProgramCe, "3.9.0")
	activate(t, root, binary.ProgramCe, "3.8.1")
	fakeInstallation(t, root, binary.ProgramTt, "3.0.0")
	fakeInstallation(t, root, binary.ProgramTt, "3.1.0")

	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{"bin/tarantool": "#!/bin/sh\n", "bin/tt": "#!/bin/sh\n"})

	stageDir := t.TempDir()

	var warnings []string

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir:    filepath.Join(t.TempDir(), "empty"),
		ProjectRoot: root,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=3.0.0,<4.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "3.9.0",
		ActiveTt:               filepath.Join(prefix, "bin", "tt"),
		ActiveTtVersion:        "3.1.0",
		Warn:                   func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, "3.8.1", bundled.Tarantool, "the active installation wins")
	assert.Equal(t, "3.1.0", bundled.Tt, "else the highest that satisfies")

	for _, msg := range warnings {
		assert.NotContains(t, msg, "bundling the active")
	}

	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "bin", "tarantool"))
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "include",
		"tarantool", "module.h"))
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tt", "bin", "tt"))
}

// TestBundleRuntimeTakesTheWholeInstallation: what tt binary installed of a
// tarantool goes into _runtime/ - the headers, share/tarantool with the
// EmmyLua stubs, the license and the list of the authors - so that tt
// package install lays out the installation tt binary would.
func TestBundleRuntimeTakesTheWholeInstallation(t *testing.T) {
	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramCe, "3.9.0")
	fakeInstallation(t, root, binary.ProgramTt, "3.0.0")
	writeTree(t, binary.InstallationPath(root, binary.ProgramCe, "3.9.0"), map[string]string{
		"share/tarantool/emmylua/box.lua": "---@meta\n",
		"LICENSE":                         "BSD-2-Clause",
		"AUTHORS":                         "the authors",
	})

	stageDir := t.TempDir()

	_, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir:    filepath.Join(t.TempDir(), "empty"),
		ProjectRoot: root,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=3.0.0,<4.0.0"),
		},
	})
	require.NoError(t, err)

	tarantool := filepath.Join(stageDir, "_runtime", "tarantool")
	assert.FileExists(t, filepath.Join(tarantool, "share", "tarantool", "emmylua", "box.lua"))
	assert.FileExists(t, filepath.Join(tarantool, "include", "tarantool", "module.h"))
	assert.FileExists(t, filepath.Join(tarantool, "LICENSE"))

	authors, err := os.ReadFile(filepath.Join(tarantool, "AUTHORS"))
	require.NoError(t, err)
	assert.Equal(t, "the authors", string(authors))
}

// TestBundleRuntimeProjectInstallationsKeepFlavorAndCache: an [ee]
// requirement takes the Enterprise installation and never the Community one,
// an SDK component is not taken, and the cache still comes first.
func TestBundleRuntimeProjectInstallationsKeepFlavorAndCache(t *testing.T) {
	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramCe, "3.9.0")
	fakeInstallation(t, root, binary.ProgramEe, "3.5.0")
	fakeInstallation(t, root, binary.ProgramEe, "sdk-gc64-3.6.0-0-r70")
	fakeInstallation(t, root, binary.ProgramTt, "3.1.0")

	cache := fakeCache(t, map[string][]string{runtimeTt: {"3.0.5"}})

	bundled, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir:    cache,
		ProjectRoot: root,
		Platform: manifest.Platform{
			Tarantool: manifest.Constraint{Version: ">=3.0.0", Flavor: flavorEE},
			Tt:        constraint(">=3.0.0"),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "3.5.0", bundled.Tarantool)
	assert.Equal(t, "3.0.5", bundled.Tt, "the cache comes before the installations")
}

// TestBundleRuntimeWarnsWithoutTarantoolLicense pins that a cache entry with
// no license file is packed and reported rather than refused: an archive that
// redistributes the binary should carry the terms it is redistributed under,
// but a bundle that ships none - the Enterprise SDK does not - must still be
// packable.
func TestBundleRuntimeWarnsWithoutTarantoolLicense(t *testing.T) {
	cache := fakeCache(t, map[string][]string{runtimeTt: {"2.0.0"}})
	writeTree(t, filepath.Join(cache, runtimeTarantool, flavorCE, "3.0.5"), map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
	})

	var warnings []string

	stage := t.TempDir()
	_, err := bundleRuntime(stage, RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		Warn: func(msg string) { warnings = append(warnings, msg) },
	})

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(stage, runtimeDirName, runtimeTarantool, "bin", "tarantool"))
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "bundling Tarantool without a license")
}

// TestBundleRuntimeFallbackFollowsLink: the active tarantool of a project is
// the link .tarantool/bin/tarantool to the executable of a tt binary
// installation, and its prefix .tarantool. The fallback bundles the LICENSE
// and the share/tarantool the installation keeps beside the executable, found
// through the link, without the warning about a missing license.
func TestBundleRuntimeFallbackFollowsLink(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".tarantool")
	installation := binary.InstallationPath(root, binary.ProgramCe, "3.8.1")
	writeTree(t, installation, map[string]string{
		"tarantool":             "#!/bin/sh\n",
		"LICENSE":               "BSD\n",
		"share/tarantool/x.lua": "return 1\n",
	})
	activate(t, root, binary.ProgramCe, "3.8.1")

	var warnings []string

	stage := t.TempDir()
	_, err := bundleRuntime(stage, RuntimeOptions{
		CacheDir: fakeCache(t, map[string][]string{runtimeTt: {"2.0.0"}}),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(root, binary.BinDirName, "tarantool"),
		ActiveTarantoolVersion: "3.8.1-0-ge643f88a5e0c",
		ActiveTarantoolFlavor:  flavorCE,
		ActiveTarantoolPrefix:  root,
		Warn:                   func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	bundled := filepath.Join(stage, runtimeDirName, runtimeTarantool)
	assert.FileExists(t, filepath.Join(bundled, "LICENSE"))
	assert.FileExists(t, filepath.Join(bundled, "share", "tarantool", "x.lua"))

	for _, warning := range warnings {
		assert.NotContains(t, warning, "without a license")
	}
}

func TestPlaceRuntimeKeepsExecutableBit(t *testing.T) {
	src := t.TempDir()
	binPath := filepath.Join(src, "bin", "tt")
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), dirPerm))
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755))

	stageDir := t.TempDir()
	require.NoError(t, placeRuntime(stageDir, runtimeSource{
		Name: runtimeTt, Version: "2.0.0", Dir: src,
	}, nil))

	info, err := os.Stat(filepath.Join(stageDir, "_runtime", "tt", "bin", "tt"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the binary must stay executable")
}
