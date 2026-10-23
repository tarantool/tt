package install

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func bundleTestParams(t *testing.T, program binary.Program) *bundleParams {
	t.Helper()

	logFile, err := os.CreateTemp(t.TempDir(), "log")
	require.NoError(t, err)

	t.Cleanup(func() { _ = logFile.Close() })

	artifactRoot := t.TempDir()

	params := &bundleParams{
		inst:         &InstallCtx{Program: program},
		artifactRoot: artifactRoot,
		tmpDir:       t.TempDir(),
		logFile:      logFile,
	}

	params.bundleInfo.Version, err = version.Parse("3.0.0")
	require.NoError(t, err)

	return params
}

func bundleTestSource(t *testing.T, params *bundleParams, headers bool) {
	t.Helper()

	root := params.tmpDir
	if params.inst.Program.BundleSource() == binary.ProgramSDK {
		root = filepath.Join(root, "tarantool-enterprise")
	}

	err := os.MkdirAll(root, 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(root, params.inst.Program.Exec()), []byte("new binary"), 0755)
	require.NoError(t, err)

	if headers {
		inc := filepath.Join(root, "include", params.inst.Program.Exec())

		require.NoError(t, os.MkdirAll(inc, 0755))

		require.NoError(t, os.WriteFile(filepath.Join(inc, "header.h"), []byte("header"), 0644))
	}
}

func TestBundleScopeArtifacts(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramEe, binary.ProgramTcm, binary.ProgramTtEe,
	} {
		t.Run(program.String(), func(t *testing.T) {
			params := bundleTestParams(t, program)
			directory := binary.InstallationPath(params.artifactRoot,
				params.inst.Program, params.bundleInfo.Version.Str)
			bundleTestSource(t, params, program.IsTarantool())

			require.NoError(t, copyNewArtifacts(params))

			_, err := validateBundleInstallation(params)
			require.NoError(t, err)

			require.NoError(t, updateSymlinks(params))

			target, err := os.Readlink(filepath.Join(params.artifactRoot, "bin", program.Exec()))
			require.NoError(t, err)
			require.Equal(t, filepath.Join(directory, program.Exec()), target)

			if program.IsTarantool() {
				target, err := os.Readlink(
					filepath.Join(params.artifactRoot, "include", program.Exec()))
				require.NoError(t, err)
				require.Equal(t, filepath.Join(directory, "include", program.Exec()), target)
			} else {
				require.NoDirExists(t, filepath.Join(params.artifactRoot, "include"))
			}

			// Reactivation does not need the source archive or extraction directory.
			require.NoError(t, os.RemoveAll(params.tmpDir))

			require.NoError(t, updateSymlinks(params))
		})
	}
}

func TestBundleValidationPreservesInstallation(t *testing.T) {
	params := bundleTestParams(t, binary.ProgramEe)
	binPath := filepath.Join(binary.InstallationPath(params.artifactRoot,
		params.inst.Program, params.bundleInfo.Version.Str), params.inst.Program.Exec())
	bundleTestSource(t, params, true)

	require.NoError(t, copyNewArtifacts(params))

	require.NoError(t, os.WriteFile(binPath, []byte("old binary"), 0755))
	require.NoError(t, os.RemoveAll(filepath.Join(params.tmpDir,
		"tarantool-enterprise", "include")))

	params.inst.Reinstall = true

	require.ErrorIs(t, copyNewArtifacts(params), errMissingBundleHeaders)

	data, err := os.ReadFile(binPath)
	require.NoError(t, err)
	require.Equal(t, "old binary", string(data))
}

func TestStandaloneBundleExecutableNames(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		program  binary.Program
		files    []string
		expected string
	}{
		{"tcm", binary.ProgramTcm, []string{"tcm"}, "tcm"},
		{"legacy tcm", binary.ProgramTcm,
			[]string{"tarantool-cluster-manager"}, "tarantool-cluster-manager"},
		{"prefer tcm", binary.ProgramTcm,
			[]string{"tarantool-cluster-manager", "tcm"}, "tcm"},
		{"missing tcm", binary.ProgramTcm, nil, ""},
		{"tt-ee ignores legacy tcm", binary.ProgramTtEe,
			[]string{"tarantool-cluster-manager"}, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			params := bundleTestParams(t, testCase.program)
			for _, name := range testCase.files {
				require.NoError(t, os.WriteFile(
					filepath.Join(params.tmpDir, name), []byte(name), 0755))
			}

			err := copyNewArtifacts(params)
			if testCase.expected == "" {
				require.ErrorIs(t, err, errCouldNotFindBinaryAt)

				return
			}

			require.NoError(t, err)
			require.NoError(t, updateSymlinks(params))

			installed := filepath.Join(binary.InstallationPath(params.artifactRoot,
				testCase.program, params.bundleInfo.Version.Str), testCase.program.Exec())
			target, err := os.Readlink(
				filepath.Join(params.artifactRoot, "bin", testCase.program.Exec()))
			require.NoError(t, err)
			require.Equal(t, installed, target)

			data, err := os.ReadFile(installed)
			require.NoError(t, err)
			require.Equal(t, testCase.expected, string(data))
			require.NoFileExists(t, filepath.Join(filepath.Dir(installed),
				"tarantool-cluster-manager"))
		})
	}
}

func TestBundleReinstallIsolationAndIncomplete(t *testing.T) {
	params := bundleTestParams(t, binary.ProgramEe)
	binPath := filepath.Join(binary.InstallationPath(params.artifactRoot,
		params.inst.Program, params.bundleInfo.Version.Str), params.inst.Program.Exec())
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0755))
	require.NoError(t, os.WriteFile(binPath, []byte("incomplete"), 0755))

	_, err := validateBundleInstallation(params)
	require.Error(t, err)

	require.ErrorIs(t, updateSymlinks(params), errIncompleteBundle)
	require.NoDirExists(t, filepath.Join(params.artifactRoot, "bin"))
	require.NoDirExists(t, filepath.Join(params.artifactRoot, "include"))

	bundleTestSource(t, params, true)

	require.ErrorIs(t, copyNewArtifacts(params), errBundlePathExists)

	other := filepath.Join(params.artifactRoot, "versions", "tarantool",
		"tarantool_3.0.0", "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(other), 0755))
	require.NoError(t, os.WriteFile(other, []byte("CE binary"), 0755))

	params.inst.Reinstall = true

	require.NoError(t, copyNewArtifacts(params))

	data, err := os.ReadFile(other)
	require.NoError(t, err)
	require.Equal(t, "CE binary", string(data))

	_, err = validateBundleInstallation(params)
	require.NoError(t, err)
}

func TestBundleTemporaryResourceRetention(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		keepTemp bool
		fail     bool
		keepLog  bool
	}{
		{name: "successful install"},
		{name: "failed install", fail: true, keepLog: true},
		{name: "keep successful files", keepTemp: true, keepLog: true},
		{name: "keep failed files", keepTemp: true, fail: true, keepLog: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			params := bundleTestParams(t, binary.ProgramTcm)

			params.inst.KeepTemp = testCase.keepTemp
			params.inst.Force = true
			params.inst.LocalRepo = t.TempDir()
			params.bundleInfo.Version.Tarball = "tcm.tar.gz"

			if !testCase.fail {
				writeTestTcmBundle(t, filepath.Join(params.inst.LocalRepo, "tcm.tar.gz"))
			}

			logPath, installErr := executeBundleInstallation(params)
			if testCase.fail {
				require.ErrorIs(t, installErr, errLocalBundleFileNotFound)
			} else {
				require.NoError(t, installErr)
			}

			t.Cleanup(func() {
				_ = os.Remove(logPath)
				_ = os.RemoveAll(params.tmpDir)
			})

			_, err := params.logFile.WriteString("closed")
			require.ErrorIs(t, err, os.ErrClosed)

			if testCase.keepLog {
				require.FileExists(t, logPath)
			} else {
				require.NoFileExists(t, logPath)
			}

			if testCase.keepTemp {
				require.DirExists(t, params.tmpDir)
			} else {
				require.NoDirExists(t, params.tmpDir)
			}
		})
	}
}

func writeTestTcmBundle(t *testing.T, path string) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)

	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	content := []byte("tcm executable")
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: "tcm", Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}))

	_, err = archive.Write(content)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, file.Close())
}
