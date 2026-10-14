package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
)

func Test_getLatestRelease(t *testing.T) {
	getFirst := func(verStr string) version.Version {
		ver, _ := version.Parse(verStr)
		return ver
	}

	versions := make([]version.Version, 0, 7)

	versions = append(versions,
		getFirst("2.10.6"),
		getFirst("2.10.7-entrypoint"),
		getFirst("2.11.0-entrypoint"),
		getFirst("2.11.0-rc1"),
		getFirst("2.11.0-rc2"),
		getFirst("3.0.0-entrypoint"),
	)

	latestRelease := getLatestRelease(versions)
	require.Equal(t, "2.10.6", latestRelease)

	versions = append(versions, getFirst("3.0.0"))
	latestRelease = getLatestRelease(versions)
	require.Equal(t, "3.0.0", latestRelease)

	latestRelease = getLatestRelease(versions[1:6])
	require.Empty(t, latestRelease)
}

func Test_installTarantoolFromBuild(t *testing.T) {
	ttBinDir := "bin"
	ttIncDir := "include"

	setupEnv := func(t *testing.T) string {
		t.Helper()

		//	├── ttBinDir
		//	├── build_ce
		//	│   └── src
		//	│       └── tarantool
		//	├── build_invalid
		//	│   └── tarantool
		//	│       └── src
		//	│           └── tarantool
		//	└── ttIncDir

		tempsDir := t.TempDir()

		ttBinDir := filepath.Join(tempsDir, ttBinDir)

		_ = os.Mkdir(ttBinDir, os.ModePerm)

		ttIncDir := filepath.Join(tempsDir, ttIncDir)

		_ = os.Mkdir(ttIncDir, os.ModePerm)

		buildDir1 := filepath.Join(tempsDir, "build_ce")

		_ = os.MkdirAll(filepath.Join(buildDir1, "src"), os.ModePerm)

		binaryPath1 := filepath.Join(buildDir1, "src/tarantool")

		_, _ = os.Create(binaryPath1)
		_ = os.Chmod(binaryPath1, 0o700)

		buildDir2 := filepath.Join(tempsDir, "build_invalid")

		_ = os.MkdirAll(filepath.Join(buildDir2, "tarantool/src"), os.ModePerm)

		binaryPath2 := filepath.Join(buildDir2, "tarantool/src/tarantool")

		_, _ = os.Create(binaryPath2)
		_ = os.Chmod(binaryPath2, 0o700)

		return tempsDir
	}

	t.Run("no include-dir", func(t *testing.T) {
		tempDirectory := setupEnv(t)

		ttBinPath := filepath.Join(tempDirectory, ttBinDir)
		ttIncPath := filepath.Join(tempDirectory, ttIncDir)

		cases := []struct {
			buildDir    string
			relExecPath string
		}{
			{filepath.Join(tempDirectory, "build_ce"), "/src/tarantool"},
			{filepath.Join(tempDirectory, "build_invalid"), "/tarantool/src/tarantool"},
		}

		for _, testCase := range cases {
			installCtx := InstallCtx{FromBuild: testCase.buildDir}
			err := installTarantoolFromBuild(installCtx, tempDirectory)
			require.NoError(t, err)

			link, err := os.Readlink(filepath.Join(ttBinPath, "tarantool"))
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(testCase.buildDir, testCase.relExecPath), link)

			// Check that old includeDir was removed.
			_, err = os.Readlink(filepath.Join(ttIncPath, "tarantool"))
			assert.Error(t, err)
		}
	})

	t.Run("with include-dir", func(t *testing.T) {
		tempDirectory := setupEnv(t)

		ttBinPath := filepath.Join(tempDirectory, ttBinDir)
		ttIncPath := filepath.Join(tempDirectory, ttIncDir)

		// Default include-dir.
		_ = os.MkdirAll(filepath.Join(tempDirectory, "build_ce", "tarantool-prefix", "include",
			"tarantool"), os.ModePerm,
		)

		// Custom include-dir.
		customIncDirectoryPath := filepath.Join(tempDirectory, "build_invalid", "custom_inc")

		_ = os.MkdirAll(customIncDirectoryPath, os.ModePerm)

		cases := []struct {
			buildDir        string
			incDir          string
			relExecPath     string
			expectedIncLink string
		}{
			{
				filepath.Join(tempDirectory, "build_ce"),
				"",
				"/src/tarantool",
				filepath.Join(tempDirectory, "build_ce", "tarantool-prefix", "include",
					"tarantool"),
			},
			{
				filepath.Join(tempDirectory, "build_invalid"),
				customIncDirectoryPath,
				"/tarantool/src/tarantool",
				filepath.Join(tempDirectory, "build_invalid", "custom_inc"),
			},
		}

		for _, testCase := range cases {
			installCtx := InstallCtx{FromBuild: testCase.buildDir, IncDir: testCase.incDir}
			err := installTarantoolFromBuild(installCtx, tempDirectory)
			require.NoError(t, err)

			execLink, err := os.Readlink(filepath.Join(ttBinPath, "tarantool"))
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(testCase.buildDir, testCase.relExecPath), execLink)

			incLink, err := os.Readlink(filepath.Join(ttIncPath, "tarantool"))
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedIncLink, incLink)
		}
	})

	t.Run("no executable", func(t *testing.T) {
		tempDirectory := setupEnv(t)

		buildDir := filepath.Join(tempDirectory, "build_ee")

		_ = os.MkdirAll(buildDir, os.ModePerm)

		installCtx := InstallCtx{FromBuild: buildDir}
		err := installTarantoolFromBuild(installCtx, tempDirectory)
		assert.Error(t, err)
	})
}

func TestSearchTarantoolHeaders(t *testing.T) {
	tempsDir := t.TempDir()

	buildEmptyPath := filepath.Join(tempsDir, "build_empty")

	_ = os.MkdirAll(buildEmptyPath, os.ModePerm)

	buildBasicPath := filepath.Join(tempsDir, "build_basic")

	_ = os.MkdirAll(filepath.Join(buildBasicPath, "tarantool-prefix", "include", "tarantool"),
		os.ModePerm)
	_ = os.MkdirAll(filepath.Join(buildBasicPath, "custom-prefix", "include", "tarantool"),
		os.ModePerm)

	buildInvalidPath := filepath.Join(tempsDir, "build_invalid")

	_ = os.MkdirAll(buildInvalidPath, os.ModePerm)
	_ = os.MkdirAll(filepath.Join(buildInvalidPath, "tarantool-prefix", "include"),
		os.ModePerm)
	_, _ = os.Create(filepath.Join(buildInvalidPath, "tarantool-prefix", "include", "tarantool"))
	_, _ = os.Create(filepath.Join(buildInvalidPath, "invalid_inc"))

	cases := []struct {
		buildDir           string
		includeDir         string
		expectedIncludeDir string
		isErr              bool
	}{
		{
			buildDir: buildBasicPath,
			expectedIncludeDir: filepath.Join(buildBasicPath, "tarantool-prefix", "include",
				"tarantool"),
		},
		{
			buildDir: buildBasicPath,
			includeDir: filepath.Join(buildBasicPath,
				"custom-prefix/include/tarantool"),
			expectedIncludeDir: filepath.Join(buildBasicPath,
				"custom-prefix/include/tarantool"),
		},
		{
			buildDir:           buildEmptyPath,
			expectedIncludeDir: "",
		},
		{
			buildDir:           buildInvalidPath,
			expectedIncludeDir: "",
		},
		{
			buildDir:           buildInvalidPath,
			includeDir:         filepath.Join(buildInvalidPath, "invalid_inc"),
			expectedIncludeDir: "",
			isErr:              true,
		},
	}

	for _, tc := range cases {
		incDir, err := searchTarantoolHeaders(tc.buildDir, tc.includeDir)
		assert.Equal(t, tc.expectedIncludeDir, incDir)

		if tc.isErr {
			assert.Error(t, err)
		}
	}
}
