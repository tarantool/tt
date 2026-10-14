package install

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/list"
	switcher "github.com/tarantool/tt/v3/internal/binary/switch"
	"github.com/tarantool/tt/v3/internal/binary/uninstall"
)

func writeTestSDK(t *testing.T, directory, selected, missing string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(directory, 0o755))

	path := filepath.Join(directory, "tarantool-enterprise-sdk-"+selected+".linux.x86_64.tar.gz")
	file, err := os.Create(path)
	require.NoError(t, err)

	gzipWriter := gzip.NewWriter(file)
	archive := tar.NewWriter(gzipWriter)
	files := map[string]string{
		"tarantool":                   "#!/bin/sh\necho tarantool\n",
		"tcm":                         "#!/bin/sh\necho 1.11.0-0-g4ecac7de8\n",
		"tt":                          "#!/bin/sh\necho 3.0.0.abcdef0\n",
		"include/tarantool/module.h":  selected,
		"rocks/manifest":              "repository = {}\n",
		"rocks/test-1.0.0-1.all.rock": "rock",
		"env.sh":                      "# Ignore the archive's environment.\nreturn 99\n",
		"VERSION":                     "TARANTOOL_EE=3.7.1\nTARANTOOL_EE_SDK=" + selected,
	}

	for name, data := range files {
		if name == missing || strings.HasPrefix(name, missing+"/") {
			continue
		}

		mode := int64(0o644)
		if name == "tarantool" || name == "tcm" || name == "tt" {
			mode = 0o755
		}

		require.NoError(t, archive.WriteHeader(&tar.Header{
			Name: "tarantool-enterprise/" + name, Mode: mode, Size: int64(len(data)),
		}))

		_, err = archive.Write([]byte(data))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, file.Close())
}

func sdkTestContext(scope binary.Scope, directory, requested string) InstallCtx {
	var ctx InstallCtx

	ctx.Scope = scope
	ctx.Program = binary.ProgramSDK
	ctx.Force = true
	ctx.LocalRepo = directory
	ctx.requestedVersion = requested

	return ctx
}

func assertSDKLinks(t *testing.T, root, selected string) {
	t.Helper()

	for link, target := range map[string]string{
		"bin/tarantool": "versions/tarantool-ee/tarantool_sdk_" + selected + "/tarantool",
		"bin/tcm":       "versions/tcm/tcm_sdk_" + selected + "/tcm",
		"bin/tt":        "versions/tt-ee/tt_sdk_" + selected + "/tt",
		"include/tarantool": "versions/tarantool-ee/tarantool_sdk_" + selected +
			"/include/tarantool",
	} {
		actual, err := filepath.EvalSymlinks(filepath.Join(root, link))
		require.NoError(t, err)
		require.Equal(t, filepath.Join(root, target), actual)
	}
}

func selectSDKTestVersion(t *testing.T, scope binary.Scope,
	program binary.Program, selected string,
) {
	t.Helper()

	var ctx switcher.SwitchCtx

	ctx.Scope, ctx.Program, ctx.Version = scope, program, selected
	require.NoError(t, switcher.Switch(&ctx))
}

func removeSDKTestVersion(t *testing.T, scope binary.Scope, selected string) {
	t.Helper()

	var ctx uninstall.UninstallCtx

	ctx.Scope, ctx.Program, ctx.Version = scope, binary.ProgramSDK, selected
	require.NoError(t, uninstall.UninstallProgram(ctx))
}

func TestSDKLifecycle(t *testing.T) {
	for _, scope := range []binary.Scope{binary.ScopeProject, binary.ScopeUser} {
		t.Run(scope.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())

			root, err := scope.ArtifactPath()
			require.NoError(t, err)

			directory := t.TempDir()
			first, second := "gc64-3.7.1-0-r93", "gc64-3.8.0-0-r94"
			writeTestSDK(t, directory, first, "")
			writeTestSDK(t, directory, second, "")

			ttDirectory := filepath.Join(root, "versions/tt/tt_v3.0.0")
			require.NoError(t, os.MkdirAll(ttDirectory, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(ttDirectory, "tt"), []byte("tt"), 0o755))
			selectSDKTestVersion(t, scope, binary.ProgramTt, "3.0.0")

			require.NoError(t, Install(sdkTestContext(scope, directory, "3.7.1")))
			assertSDKLinks(t, root, first)
			require.NoDirExists(t, filepath.Join(root, "versions/tt/tt_sdk_"+first))
			require.NoDirExists(t, filepath.Join(root, "versions/tarantool-ee/tarantool-ee_3.7.1"))

			require.NoFileExists(t, filepath.Join(root, "env.sh"))
			require.NoDirExists(t, filepath.Join(root, "rocks"))

			require.NoError(t, Install(sdkTestContext(scope, directory, "")))
			assertSDKLinks(t, root, second)

			// A full SDK switch restores the selected versions of every component.
			selectSDKTestVersion(t, scope, binary.ProgramSDK, "sdk-3.7.1")
			assertSDKLinks(t, root, first)
			selectSDKTestVersion(t, scope, binary.ProgramTcm, "sdk-3.8.0")
			removeSDKTestVersion(t, scope, "3.8.0")
			assertSDKLinks(t, root, first)

			// Ordinary EE and SDK versions can coexist, even for the same archive.
			eeContext := sdkTestContext(scope, directory, first)

			eeContext.Program = binary.ProgramEe
			require.NoError(t, Install(eeContext))
			require.DirExists(t, filepath.Join(root, "versions/tarantool-ee/tarantool-ee_3.7.1"))
			selectSDKTestVersion(t, scope, binary.ProgramSDK, first)

			// Reinstalling an existing SDK activates it without extracting the archive again.
			require.NoError(t, os.RemoveAll(directory))

			params := &bundleParams{inst: &eeContext, artifactRoot: root}

			params.inst.Program = binary.ProgramSDK
			params.bundleInfo.Version.Str = first

			done, err := prepareExistingBundle(params)
			require.NoError(t, err)
			require.True(t, done)

			entries, err := os.ReadDir(filepath.Join(root, "versions/tarantool-sdk"))
			require.NoError(t, err)

			versions, err := list.ParseBinaries(entries, binary.ProgramSDK, root)
			require.NoError(t, err)
			require.Len(t, versions, 1)
			require.Equal(t, first+" [active]", versions[0].Str)

			standaloneTCM := filepath.Join(root, "versions/tcm/tcm_1.3.1/tcm")
			require.NoError(t, os.MkdirAll(filepath.Dir(standaloneTCM), 0o755))
			require.NoError(t, os.WriteFile(standaloneTCM, []byte("tcm"), 0o755))
			selectSDKTestVersion(t, scope, binary.ProgramTcm, "1.3.1")
			removeSDKTestVersion(t, scope, "sdk-3.7.1")
			require.NoFileExists(t, filepath.Join(root, "env.sh"))
			require.NoDirExists(t, filepath.Join(root, "rocks"))
			require.NoDirExists(t, filepath.Join(root, "versions/tcm/tcm_sdk_"+first))
			require.NoDirExists(t,
				filepath.Join(root, "versions/tarantool-ee/tarantool_sdk_"+first))

			activeTCM, err := filepath.EvalSymlinks(filepath.Join(root, "bin/tcm"))
			require.NoError(t, err)
			require.Equal(t, standaloneTCM, activeTCM)

			activeTT, err := filepath.EvalSymlinks(filepath.Join(root, "bin/tt"))
			require.NoError(t, err)
			require.Equal(t, filepath.Join(ttDirectory, "tt"), activeTT)
		})
	}
}

func TestSDKInvalidReinstallPreservesProducts(t *testing.T) {
	for _, missing := range []string{"tarantool", "tcm", "tt", "include/tarantool/module.h"} {
		t.Run(missing, func(t *testing.T) {
			t.Chdir(t.TempDir())

			directory := t.TempDir()
			selected := "gc64-3.7.1-0-r93"
			writeTestSDK(t, directory, selected, "")

			ctx := sdkTestContext(binary.ScopeProject, directory, selected)
			require.NoError(t, Install(ctx))
			writeTestSDK(t, directory, selected, missing)

			ctx.Reinstall = true
			require.Error(t, Install(ctx))

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)
			assertSDKLinks(t, root, selected)

			sdk, err := binary.ReadSDK(root, selected)
			require.NoError(t, err)
			require.NoError(t, binary.ValidateSDKManifest(sdk, selected))
			require.NoError(t, binary.ValidateSDKFiles(root, sdk))
		})
	}
}

func TestSDKSwitchValidatesEntireSet(t *testing.T) {
	t.Chdir(t.TempDir())

	directory := t.TempDir()
	first, second := "gc64-3.7.1-0-r93", "gc64-3.8.0-0-r94"
	writeTestSDK(t, directory, first, "")
	writeTestSDK(t, directory, second, "")
	require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, first)))
	require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, second)))

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(root, "versions/tcm/tcm_sdk_"+first, "tcm")))

	var ctx switcher.SwitchCtx

	ctx.Scope, ctx.Program, ctx.Version = binary.ScopeProject, binary.ProgramSDK, first
	require.Error(t, switcher.Switch(&ctx))
	assertSDKLinks(t, root, second)
	removeSDKTestVersion(t, binary.ScopeProject, first)
	assertSDKLinks(t, root, second)
}

func TestSDKInvalidManifestPreservesInstallations(t *testing.T) {
	const (
		first  = "gc64-3.7.1-0-r93"
		second = "gc64-3.8.0-0-r94"
	)

	for _, testCase := range []struct {
		name     string
		version  string
		products []string
	}{
		{"different owner version", second, []string{"tarantool-ee", "tcm"}},
		{"unsafe version", "../outside", []string{"tarantool-ee", "tcm"}},
		{"empty products", first, []string{}},
		{"unsupported component", first, []string{"tarantool"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			directory := t.TempDir()
			writeTestSDK(t, directory, first, "")
			writeTestSDK(t, directory, second, "")
			require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, first)))
			require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, second)))

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			manifest, err := json.Marshal(map[string]any{
				"version": testCase.version, "products": testCase.products,
			})
			require.NoError(t, err)

			manifestPath := filepath.Join(binary.InstallationPath(root, binary.ProgramSDK, first),
				binary.SDKManifestName)
			require.NoError(t, os.WriteFile(manifestPath, manifest, 0o644))

			var switchCtx switcher.SwitchCtx

			switchCtx.Scope = binary.ScopeProject
			switchCtx.Program = binary.ProgramSDK
			switchCtx.Version = first
			require.ErrorIs(t, switcher.Switch(&switchCtx), binary.ErrInvalidSDK)
			assertSDKLinks(t, root, second)

			var uninstallCtx uninstall.UninstallCtx

			uninstallCtx.Scope = binary.ScopeProject
			uninstallCtx.Program = binary.ProgramSDK
			uninstallCtx.Version = first
			require.ErrorIs(t, uninstall.UninstallProgram(uninstallCtx), binary.ErrInvalidSDK)
			assertSDKLinks(t, root, second)

			for _, product := range binary.SDKProducts() {
				require.DirExists(t,
					binary.InstallationPath(root, product, binary.SDKVersionPrefix+first))
			}

			entries, err := os.ReadDir(filepath.Join(root, "versions", binary.ProgramSDK.String()))
			require.NoError(t, err)

			_, err = list.ParseBinaries(entries, binary.ProgramSDK, root)
			require.ErrorIs(t, err, binary.ErrInvalidSDK)
		})
	}
}

func TestSDKComponentFallbackUsesProductVersion(t *testing.T) {
	for _, standalone := range []string{"1.10.0", "1.12.0"} {
		t.Run(standalone, func(t *testing.T) {
			t.Chdir(t.TempDir())

			directory := t.TempDir()
			selected := "gc64-3.7.1-0-r93"
			writeTestSDK(t, directory, selected, "")
			require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, selected)))

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			for _, release := range []string{standalone, "1.13.0"} {
				path := binary.InstallationPath(root, binary.ProgramTcm, release)
				require.NoError(t, os.MkdirAll(path, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(path, "tcm"),
					[]byte("#!/bin/sh\necho "+release+"\n"), 0o755))
			}

			selectSDKTestVersion(t, binary.ScopeProject, binary.ProgramTcm, "1.13.0")

			var ctx uninstall.UninstallCtx

			ctx.Scope, ctx.Program, ctx.Version = binary.ScopeProject, binary.ProgramTcm, "1.13.0"
			require.NoError(t, uninstall.UninstallProgram(ctx))

			expected := standalone
			if standalone == "1.10.0" {
				expected = binary.SDKVersionPrefix + selected // Bundled TCM is 1.11.0.
			}

			active, err := filepath.EvalSymlinks(filepath.Join(root, "bin/tcm"))
			require.NoError(t, err)

			expectedPath := filepath.Join(
				binary.InstallationPath(root, binary.ProgramTcm, expected), "tcm")
			require.Equal(t, expectedPath, active)
		})
	}
}

func TestSDKUninstallSkipsIncompleteCandidates(t *testing.T) {
	for _, manifest := range []string{"missing", "invalid JSON", "unreadable"} {
		t.Run(manifest, func(t *testing.T) {
			t.Chdir(t.TempDir())

			directory := t.TempDir()
			selected := "gc64-3.7.1-0-r93"
			writeTestSDK(t, directory, selected, "")
			require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, selected)))

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			incomplete := binary.InstallationPath(root, binary.ProgramSDK, "gc64-3.9.0-0-r95")
			require.NoError(t, os.MkdirAll(incomplete, 0o755))

			switch manifest {
			case "invalid JSON":
				require.NoError(t, os.WriteFile(filepath.Join(incomplete, binary.SDKManifestName),
					[]byte("{"), 0o644))
			case "unreadable":
				require.NoError(t, os.Symlink(binary.SDKManifestName,
					filepath.Join(incomplete, binary.SDKManifestName)))
			}

			fallback := binary.InstallationPath(root, binary.ProgramTcm, "1.12.0")
			require.NoError(t, os.MkdirAll(fallback, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(fallback, "tcm"), []byte("tcm"), 0o755))

			removeSDKTestVersion(t, binary.ScopeProject, selected)

			active, err := filepath.EvalSymlinks(filepath.Join(root, "bin/tcm"))
			require.NoError(t, err)
			require.Equal(t, filepath.Join(fallback, "tcm"), active)
			require.NoFileExists(t, filepath.Join(root, "bin/tarantool"))
			require.NoDirExists(t, binary.InstallationPath(root, binary.ProgramSDK, selected))
		})
	}
}

func TestSDKUninstallIgnoresReplacementActivationFailure(t *testing.T) {
	t.Chdir(t.TempDir())

	directory := t.TempDir()
	first, second := "gc64-3.7.1-0-r93", "gc64-3.8.0-0-r94"
	writeTestSDK(t, directory, first, "")
	writeTestSDK(t, directory, second, "")
	require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, second)))
	require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, first)))

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	// A conflicting regular file prevents activation of the replacement SDK.
	headers := filepath.Join(root, "include/tarantool")
	require.NoError(t, os.Remove(headers))
	require.NoError(t, os.WriteFile(headers, []byte("user file"), 0o644))

	var ctx uninstall.UninstallCtx

	ctx.Scope, ctx.Program, ctx.Version = binary.ScopeProject, binary.ProgramSDK, first
	require.NoError(t, uninstall.UninstallProgram(ctx))

	for _, program := range binary.SDKProducts() {
		require.NoDirExists(t,
			binary.InstallationPath(root, program, binary.SDKVersionPrefix+first))
	}

	require.NoDirExists(t, binary.InstallationPath(root, binary.ProgramSDK, first))

	_, err = os.Lstat(filepath.Join(root, "bin/tarantool"))
	require.ErrorIs(t, err, os.ErrNotExist)

	active, err := filepath.EvalSymlinks(filepath.Join(root, "bin/tcm"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(binary.InstallationPath(root, binary.ProgramTcm,
		binary.SDKVersionPrefix+second), "tcm"), active)

	data, err := os.ReadFile(headers)
	require.NoError(t, err)
	require.Equal(t, "user file", string(data))
}

func TestSDKUninstallIgnoresUnusableFallbackDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	directory := t.TempDir()
	selected := "gc64-3.7.1-0-r93"
	writeTestSDK(t, directory, selected, "")
	require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, selected)))

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "versions/tarantool")))

	fallback := binary.InstallationPath(root, binary.ProgramTcm, "1.12.0")
	require.NoError(t, os.MkdirAll(fallback, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fallback, "tcm"), []byte("tcm"), 0o755))

	removeSDKTestVersion(t, binary.ScopeProject, selected)
	require.NoDirExists(t, binary.InstallationPath(root, binary.ProgramSDK, selected))
	require.DirExists(t, outside)

	active, err := filepath.EvalSymlinks(filepath.Join(root, "bin/tcm"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(fallback, "tcm"), active)
}

func TestSDKIgnoresArchiveExtras(t *testing.T) {
	for _, missing := range []string{"", "env.sh", "rocks", "rocks/manifest"} {
		t.Run("missing="+missing, func(t *testing.T) {
			project := filepath.Join(t.TempDir(), "project with spaces")
			require.NoError(t, os.Mkdir(project, 0o755))
			t.Chdir(project)

			directory := t.TempDir()
			selected := "gc64-3.7.1-0-r93"
			writeTestSDK(t, directory, selected, missing)
			require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, "")))

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)
			assertSDKLinks(t, root, selected)
			require.NoFileExists(t, filepath.Join(root, "env.sh"))
			require.NoDirExists(t, filepath.Join(root, "rocks"))

			for _, program := range binary.SDKProducts() {
				installation := binary.InstallationPath(root, program,
					binary.SDKVersionPrefix+selected)
				require.NoFileExists(t, filepath.Join(installation, "env.sh"))
				require.NoDirExists(t, filepath.Join(installation, "rocks"))
			}
		})
	}
}

func TestSDKLifecyclePreservesUnmanagedPaths(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Chdir(t.TempDir())

			scope := binary.ScopeProject
			root, err := scope.ArtifactPath()
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(root, 0o755))

			original := []byte("#!/usr/bin/env bash\n" +
				"# Generated by tt binary for this scope.\nkeep\n")
			paths := make(map[string]string)
			targets := make(map[string]string)

			for _, name := range []string{"env.sh", "rocks"} {
				path := filepath.Join(root, name)

				switch kind {
				case "directory":
					require.NoError(t, os.Mkdir(path, 0o755))

					path = filepath.Join(path, "keep")
				case "symlink":
					target := filepath.Join(t.TempDir(), name)
					require.NoError(t, os.Symlink(target, path))

					targets[path] = target
				}

				require.NoError(t, os.WriteFile(path, original, 0o644))

				paths[path] = string(original)
			}

			directory := t.TempDir()
			first, second := "gc64-3.7.1-0-r93", "gc64-3.8.0-0-r94"

			for _, selected := range []string{first, second} {
				writeTestSDK(t, directory, selected, "")
				require.NoError(t, Install(sdkTestContext(scope, directory, selected)))
			}

			require.NoError(t, Install(sdkTestContext(scope, directory, second)))
			selectSDKTestVersion(t, scope, binary.ProgramSDK, first)
			selectSDKTestVersion(t, scope, binary.ProgramTcm, binary.SDKVersionPrefix+second)
			removeSDKTestVersion(t, scope, second)
			assertSDKLinks(t, root, first)
			removeSDKTestVersion(t, scope, first)

			for path, expected := range paths {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, expected, string(data))

				if kind == "symlink" {
					target, err := os.Readlink(path)
					require.NoError(t, err)
					require.Equal(t, targets[path], target)
				}
			}
		})
	}
}

func TestSDKVersionSelectionAndNamespaces(t *testing.T) {
	bundles := eeTestBundles(t, "gc64-3.7.1-0-r9", "gc64-3.7.1-0-r93",
		"gc64-3.8.0-rc1-0-r94", "nogc64-3.9.0-0-r95")
	for _, request := range []string{"", "3", "3.7", "3.7.1", "sdk-3.7.1"} {
		selected, err := selectBundleVersion(bundles, request, binary.ProgramSDK)
		require.NoError(t, err)
		require.Equal(t, "gc64-3.7.1-0-r93", selected.Version.Str)
	}

	installed := []string{"3.7.1", "sdk-gc64-3.7.1-0-r93", "sdk-gc64-3.8.0-0-r94"}
	selected, err := binary.MatchInstalledVersion(binary.ProgramTcm, "3.7.1", installed)
	require.NoError(t, err)
	require.Equal(t, "3.7.1", selected)

	selected, err = binary.MatchInstalledVersion(binary.ProgramTcm, "sdk-3.7.1", installed)
	require.NoError(t, err)
	require.Equal(t, "sdk-gc64-3.7.1-0-r93", selected)

	_, err = binary.MatchInstalledVersion(binary.ProgramTcm, "3.8.0", installed)
	require.ErrorIs(t, err, version.ErrInstalledVersionNotFound)

	_, err = binary.MatchInstalledVersion(binary.ProgramSDK, "3.7.1",
		[]string{"gc64-3.7.1-0-r9", "gc64-3.7.1-0-r93"})
	require.ErrorIs(t, err, version.ErrAmbiguousInstalledVersion)
}
