package install

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func bundleTestParams(t *testing.T, program binary.Program) *bundleParams {
	t.Helper()

	logFile, err := os.CreateTemp(t.TempDir(), "log")
	require.NoError(t, err)

	t.Cleanup(func() { _ = logFile.Close() })

	artifactRoot := t.TempDir()
	prgVersion := program.String() + "_3.0.0"
	versionDir := filepath.Join(artifactRoot, "versions", program.String(), prgVersion)

	return &bundleParams{
		inst:         &InstallCtx{Program: program},
		artifactRoot: artifactRoot,
		versionDir:   versionDir,
		binPath:      filepath.Join(versionDir, program.Exec()),
		incPath:      filepath.Join(versionDir, "inc", program.Exec()),
		tmpDir:       t.TempDir(),
		prgVersion:   prgVersion,
		logFile:      logFile,
	}
}

func bundleTestSource(t *testing.T, params *bundleParams, headers bool) {
	t.Helper()

	root := params.tmpDir
	if params.inst.Program == binary.ProgramEe {
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
	for _, program := range []binary.Program{binary.ProgramEe, binary.ProgramTcm} {
		t.Run(program.String(), func(t *testing.T) {
			params := bundleTestParams(t, program)
			bundleTestSource(t, params, program.IsTarantool())

			require.NoError(t, copyNewArtifacts(params))

			require.True(t, checkExistingInstallation(params))

			require.NoError(t, changeActiveBundleVersion(params))

			target, err := os.Readlink(filepath.Join(params.artifactRoot, "bin", program.Exec()))
			require.NoError(t, err)
			require.Equal(t, params.binPath, target)

			if program.IsTarantool() {
				target, err := os.Readlink(
					filepath.Join(params.artifactRoot, "inc", program.Exec()))
				require.NoError(t, err)
				require.Equal(t, params.incPath, target)
			} else {
				require.NoDirExists(t, filepath.Join(params.artifactRoot, "inc"))
			}

			// Reactivation does not need the source archive or extraction directory.
			require.NoError(t, os.RemoveAll(params.tmpDir))

			require.NoError(t, changeActiveBundleVersion(params))
		})
	}
}

func TestBundleValidationPreservesInstallation(t *testing.T) {
	params := bundleTestParams(t, binary.ProgramEe)
	bundleTestSource(t, params, true)

	require.NoError(t, copyNewArtifacts(params))

	require.NoError(t, os.WriteFile(params.binPath, []byte("old binary"), 0755))
	require.NoError(t, os.RemoveAll(filepath.Join(params.tmpDir,
		"tarantool-enterprise", "include")))

	params.inst.Reinstall = true

	require.ErrorIs(t, copyNewArtifacts(params), errMissingBundleHeaders)

	data, err := os.ReadFile(params.binPath)
	require.NoError(t, err)
	require.Equal(t, "old binary", string(data))
}

func TestBundleReinstallIsolationAndIncomplete(t *testing.T) {
	params := bundleTestParams(t, binary.ProgramEe)
	require.NoError(t, os.MkdirAll(filepath.Dir(params.binPath), 0755))
	require.NoError(t, os.WriteFile(params.binPath, []byte("incomplete"), 0755))

	require.False(t, checkExistingInstallation(params))

	require.ErrorIs(t, changeActiveBundleVersion(params), errIncompleteBundle)
	require.NoDirExists(t, filepath.Join(params.artifactRoot, "bin"))
	require.NoDirExists(t, filepath.Join(params.artifactRoot, "inc"))

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

	require.True(t, checkExistingInstallation(params))
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
