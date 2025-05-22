package install

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/version"
)

func Test_dirsAreWriteable(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	tmpDirNonWriteableForAll := t.TempDir()
	// dr-xr-xr-x mode.
	permissions := 0o555
	require.NoError(t, os.Chmod(tmpDirNonWriteableForAll, os.FileMode(permissions)))

	tmpDirNonWriteableExceptOwner := t.TempDir()
	// drwxr-xr-x mode.
	permissions = 0o755
	require.NoError(t, os.Chmod(tmpDirNonWriteableExceptOwner, os.FileMode(permissions)))

	type args struct {
		dir string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Checking access to non writeable directory",
			args: args{dir: tmpDirNonWriteableForAll},
			want: false,
		},
		{
			name: "Checking access to non writeable directory except of owner",
			args: args{dir: tmpDirNonWriteableExceptOwner},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, dirIsWritable(tt.args.dir),
				"dirIsWriteable(%v)", tt.args.dir)
		})
	}
}

func Test_subDirIsWritable(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	tmpDirNonWriteableForAll := t.TempDir()
	// dr-xr-xr-x mode.
	permissions := 0o555
	require.NoError(t, os.Chmod(tmpDirNonWriteableForAll, os.FileMode(permissions)))

	tmpDirNonWriteableExceptOwner := t.TempDir()
	// drwxr-xr-x mode.
	permissions = 0o755
	require.NoError(t, os.Chmod(tmpDirNonWriteableExceptOwner, os.FileMode(permissions)))

	type args struct {
		dir string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Subdirectory is writeable",
			args: args{dir: filepath.Join(tmpDirNonWriteableExceptOwner, "test", "test")},
			want: true,
		},
		{
			name: "Subdirectory is not writeable",
			args: args{dir: filepath.Join(tmpDirNonWriteableForAll, "test", "test")},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, subDirIsWritable(tt.args.dir), "subDirIsWritable(%v)",
				tt.args.dir)
		})
	}
}

func Test_getLatestRelease(t *testing.T) {
	getFirst := func(verStr string) version.Version {
		ver, _ := version.Parse(verStr)
		return ver
	}

	versions := []version.Version{
		getFirst("2.10.6"),
		getFirst("2.10.7-entrypoint"),
		getFirst("2.11.0-entrypoint"),
		getFirst("2.11.0-rc1"),
		getFirst("2.11.0-rc2"),
		getFirst("3.0.0-entrypoint"),
	}

	latestRelease := getLatestRelease(versions)
	require.Equal(t, "2.10.6", latestRelease)

	versions = append(versions, getFirst("3.0.0"))
	latestRelease = getLatestRelease(versions)
	require.Equal(t, "3.0.0", latestRelease)

	latestRelease = getLatestRelease(versions[1:6])
	require.Equal(t, "", latestRelease)
}

func Test_installTarantoolDev(t *testing.T) {
	ttBinDir := "binDir"
	ttIncDir := "incDir"

	setupEnv := func() string {
		//	├── ttBinDir
		//	├── build_ce
		//	│   └── src
		//	│       └── tarantool
		//	├── build_invalid
		//	│   └── tarantool
		//	│       └── src
		//	│           └── tarantool
		//	└── ttIncDir

		tempDir := os.TempDir()
		tempsDir, _ := os.MkdirTemp(tempDir, "install_tarantool_dev_test")

		ttBinDir := filepath.Join(tempsDir, ttBinDir)
		os.Mkdir(ttBinDir, os.ModePerm)

		ttIncDir := filepath.Join(tempsDir, ttIncDir)
		os.Mkdir(ttIncDir, os.ModePerm)

		buildDir1 := filepath.Join(tempsDir, "build_ce")
		os.MkdirAll(filepath.Join(buildDir1, "src"), os.ModePerm)
		binaryPath1 := filepath.Join(buildDir1, "src/tarantool")
		os.Create(binaryPath1)
		os.Chmod(binaryPath1, 0o700)

		buildDir2 := filepath.Join(tempsDir, "build_invalid")
		os.MkdirAll(filepath.Join(buildDir2, "tarantool/src"), os.ModePerm)
		binaryPath2 := filepath.Join(buildDir2, "tarantool/src/tarantool")
		os.Create(binaryPath2)
		os.Chmod(binaryPath2, 0o700)

		return tempsDir
	}

	t.Run("no include-dir", func(t *testing.T) {
		tempDirectory := setupEnv()
		defer os.RemoveAll(tempDirectory)

		ttBinPath := filepath.Join(tempDirectory, ttBinDir)
		ttIncPath := filepath.Join(tempDirectory, ttIncDir)

		cases := []struct {
			buildDir    string
			relExecPath string
		}{
			{filepath.Join(tempDirectory, "build_ce"), "/src/tarantool"},
			{filepath.Join(tempDirectory, "build_invalid"), "/tarantool/src/tarantool"},
		}

		for _, tc := range cases {
			err := installTarantoolDev(ttBinPath, ttIncPath, tc.buildDir, "")
			assert.NoError(t, err)
			link, err := os.Readlink(filepath.Join(ttBinPath, "tarantool"))
			assert.NoError(t, err)
			assert.Equal(t, filepath.Join(tc.buildDir, tc.relExecPath), link)

			// Check that old includeDir was removed.
			_, err = os.Readlink(filepath.Join(ttIncPath, "tarantool"))
			assert.Error(t, err)
		}
	})

	t.Run("with include-dir", func(t *testing.T) {
		tempDirectory := setupEnv()
		defer os.RemoveAll(tempDirectory)

		ttBinPath := filepath.Join(tempDirectory, ttBinDir)
		ttIncPath := filepath.Join(tempDirectory, ttIncDir)

		// Default include-dir.
		os.MkdirAll(filepath.Join(tempDirectory, "build_ce", "tarantool-prefix", "include",
			"tarantool"), os.ModePerm,
		)

		// Custom include-dir.
		customIncDirectoryPath := filepath.Join(tempDirectory, "build_invalid", "custom_inc")
		os.MkdirAll(customIncDirectoryPath, os.ModePerm)
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

		for _, tc := range cases {
			err := installTarantoolDev(ttBinPath, ttIncPath, tc.buildDir, tc.incDir)
			assert.NoError(t, err)
			execLink, err := os.Readlink(filepath.Join(ttBinPath, "tarantool"))
			assert.NoError(t, err)
			assert.Equal(t, execLink, filepath.Join(tc.buildDir, tc.relExecPath))

			incLink, err := os.Readlink(filepath.Join(ttIncPath, "tarantool"))
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedIncLink, incLink)
		}
	})

	t.Run("no executable", func(t *testing.T) {
		tempDirectory := setupEnv()
		defer os.RemoveAll(tempDirectory)

		ttBinPath := filepath.Join(tempDirectory, ttBinDir)
		ttIncPath := filepath.Join(tempDirectory, ttIncDir)

		buildDir := filepath.Join(tempDirectory, "build_ee")
		os.MkdirAll(buildDir, os.ModePerm)
		err := installTarantoolDev(ttBinPath, ttIncPath, buildDir, "")
		assert.Error(t, err)
	})
}

func TestSearchTarantoolHeaders(t *testing.T) {
	tempDir := os.TempDir()
	tempsDir, _ := os.MkdirTemp(tempDir, "search_tarantool_headers_test")
	defer os.RemoveAll(tempsDir)

	buildEmptyPath := filepath.Join(tempsDir, "build_empty")
	os.MkdirAll(buildEmptyPath, os.ModePerm)

	buildBasicPath := filepath.Join(tempsDir, "build_basic")
	os.MkdirAll(filepath.Join(buildBasicPath, "tarantool-prefix", "include", "tarantool"),
		os.ModePerm)
	os.MkdirAll(filepath.Join(buildBasicPath, "custom-prefix", "include", "tarantool"),
		os.ModePerm)

	buildInvalidPath := filepath.Join(tempsDir, "build_invalid")
	os.MkdirAll(buildInvalidPath, os.ModePerm)
	os.MkdirAll(filepath.Join(buildInvalidPath, "tarantool-prefix", "include"),
		os.ModePerm)
	os.Create(filepath.Join(buildInvalidPath, "tarantool-prefix", "include", "tarantool"))
	os.Create(filepath.Join(buildInvalidPath, "invalid_inc"))

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
