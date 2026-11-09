package customerzone_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/customerzone"
)

// recordLog makes slog.Default record every log record, the facade's
// included, until the test ends, and returns the recorder.
func recordLog(t *testing.T) *logtest.Recorder {
	t.Helper()

	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	return recorder
}

// linuxQuery asks for the release builds of program for Linux amd64.
func linuxQuery(program binary.Program, line string) bundle.Query {
	return bundle.Query{
		Product: program.String(),
		OS:      "linux",
		Arch:    "amd64",
		Channel: bundle.ChannelRelease,
		Version: line,
	}
}

// makeDir creates a directory with empty files and subdirectories named dirs.
func makeDir(t *testing.T, files, dirs []string) string {
	t.Helper()

	dir := t.TempDir()

	for _, name := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o644))
	}

	for _, name := range dirs {
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o755))
	}

	return dir
}

func TestDirList(t *testing.T) {
	tests := map[string]struct {
		program         binary.Program
		files           []string
		dirs            []string
		missing         bool
		expectedVersion version.VersionSlice
		errMsg          string
		logMsg          string
	}{
		"No matching files": {
			program: binary.ProgramEe,
			files:   []string{"random-file.txt", "another-file.log"},
			logMsg:  fmt.Sprintf("No local archives found for %q", binary.ProgramEe),
		},
		"Not exists directory": {
			program: binary.ProgramEe,
			missing: true,
			logMsg:  "Directory not found, cannot search for local archives",
		},
		"Matching files for " + binary.ProgramEe.String(): {
			program: binary.ProgramEe,
			files: []string{
				"tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
				"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r5.linux.x86_64.tar.gz",
				"tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz.sha256",
				"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r5.linux.x86_64.tar.gz.sha256",
				"tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz",
				"tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz",
				"tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz.sha256",
				"tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz.sha256",
			},
			// Tarantool 2.x archives are not offered.
			expectedVersion: version.VersionSlice{
				{
					Tarball:   "tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r5.linux.x86_64.tar.gz",
					Major:     3,
					Minor:     3,
					Patch:     2,
					Revision:  5,
					Release:   version.Release{Type: version.TypeRelease},
					Str:       "debug-gc64-3.3.2-0-r5",
					BuildName: "debug-gc64",
				},
				{
					Tarball:   "tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
					Major:     3,
					Minor:     3,
					Patch:     2,
					Revision:  59,
					Release:   version.Release{Type: version.TypeRelease},
					Str:       "gc64-3.3.2-0-r59",
					BuildName: "gc64",
				},
			},
		},
		"Directory with match name": {
			program: binary.ProgramEe,
			files:   []string{"tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz"},
			dirs: []string{
				"tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz",
				"tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz",
			},
			expectedVersion: version.VersionSlice{
				{
					Tarball:   "tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
					Major:     3,
					Minor:     3,
					Patch:     2,
					Revision:  59,
					Release:   version.Release{Type: version.TypeRelease},
					Str:       "gc64-3.3.2-0-r59",
					BuildName: "gc64",
				},
			},
		},
		"Matching files for " + binary.ProgramTcm.String(): {
			program: binary.ProgramTcm,
			files: []string{
				"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
				"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz.sha256",
				"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
				"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz.sha256",
			},
			expectedVersion: version.VersionSlice{
				{
					Tarball: "tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
					Major:   1,
					Minor:   3,
					Patch:   0,
					Release: version.Release{Type: version.TypeRelease},
					Hash:    "g3857712a",
					Str:     "1.3.0-0-g3857712a",
				},
				{
					Tarball: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
					Major:   1,
					Minor:   3,
					Patch:   1,
					Release: version.Release{Type: version.TypeRelease},
					Hash:    "g074b5ffa",
					Str:     "1.3.1-0-g074b5ffa",
				},
			},
		},
		"Unsupported program": {
			program: binary.ProgramUnknown,
			files:   []string{"unsupported-program-1.0.0.tar.gz"},
			errMsg: `not a product of the customer zone: "` +
				binary.ProgramUnknown.String() + `"`,
		},
		"Community edition": {
			program: binary.ProgramCe,
			files:   []string{"tarantool-3.4.0.tar.gz"},
			errMsg:  `not a product of the customer zone: "tarantool"`,
		},
		"Invalid version format for " + binary.ProgramEe.String(): {
			program: binary.ProgramEe,
			files:   []string{"tarantool-enterprise-sdk-gc64-Ver3.1.linux.x86_64.tar.gz"},
			errMsg:  "failed to parse version from file",
		},
		"Invalid version format for " + binary.ProgramTcm.String(): {
			program: binary.ProgramTcm,
			files:   []string{"tcm-1.3.1-X-g074b5ffa.linux.amd64.tar.gz"},
			errMsg:  "failed to parse version from file",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := recordLog(t)

			dir := makeDir(t, tt.files, tt.dirs)
			if tt.missing {
				dir = filepath.Join(dir, "missing")
			}

			bundles, err := binary.ListBundles(t.Context(), customerzone.NewDir(dir),
				tt.program, linuxQuery(tt.program, ""))
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg)

				return
			}

			require.NoError(t, err)
			require.Len(t, bundles, len(tt.expectedVersion))

			for i, found := range bundles {
				require.Equal(t, tt.expectedVersion[i], found.Version, "index %d", i)
				require.Equal(t, filepath.Join(dir, found.Version.Tarball), found.Artifact.Locator)
			}

			if tt.logMsg != "" {
				found := false

				for _, message := range recorder.Messages() {
					if strings.Contains(message, tt.logMsg) {
						found = true

						break
					}
				}

				require.True(t, found, "expected %q not found in log entries", tt.logMsg)
			}
		})
	}
}

func TestDirListUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}

	dir := makeDir(t, []string{"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz"}, nil)
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := customerzone.NewDir(dir).List(t.Context(), linuxQuery(binary.ProgramTcm, ""))
	require.ErrorContains(t, err, "failed to read directory")
}

func TestDirListTtEe(t *testing.T) {
	dir := makeDir(t, []string{
		"tt-ee-2.14.0-linux-amd64.tar.gz",
		"tt-ee-2.15.0-linux-amd64.tar.gz",
		"tt-ee-2.15.0-linux-arm64.tar.gz",
		"tt-ee-2.15.0-linux-amd64.tar.gz.sha256",
		"tt-ee-2.15.0-darwin-amd64.tar.gz",
		"tt-ee-2.15.0-darwin-arm64.tar.gz",
		"tcm-1.3.0.linux.amd64.tar.gz",
		"tarantool-enterprise-sdk-gc64-3.8.0-0-r94.linux.x86_64.tar.gz",
	}, nil)
	source := customerzone.NewDir(dir)

	bundles, err := binary.ListBundles(t.Context(), source, binary.ProgramTtEe,
		linuxQuery(binary.ProgramTtEe, "2.15"))
	require.NoError(t, err)
	require.Len(t, bundles, 1)
	require.Equal(t, "2.15.0", bundles[0].Version.Str)
	require.Equal(t, "tt-ee-2.15.0-linux-amd64.tar.gz", bundles[0].Version.Tarball)

	bundles, err = binary.ListBundles(t.Context(), source, binary.ProgramTtEe,
		linuxQuery(binary.ProgramTtEe, ""))
	require.NoError(t, err)
	require.Len(t, bundles, 2)
	require.Equal(t, "2.14.0", bundles[0].Version.Str)
	require.Equal(t, "2.15.0", bundles[1].Version.Str)

	query := linuxQuery(binary.ProgramTtEe, "")

	query.OS = "darwin"

	_, err = source.List(t.Context(), query)
	require.ErrorContains(t, err, "unsupported OS: darwin")

	query = linuxQuery(binary.ProgramTtEe, "")
	query.Arch = "arm64"

	_, err = source.List(t.Context(), query)
	require.ErrorContains(t, err, "unsupported architecture: arm64")
}

func TestDirFetch(t *testing.T) {
	const (
		tarball = "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz"
		body    = "the bytes of the archive"
	)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, tarball), []byte(body), 0o644))

	source := customerzone.NewDir(dir)
	require.Equal(t, "local-dir", source.Name())

	artifacts, err := source.List(t.Context(), linuxQuery(binary.ProgramTcm, ""))
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	var dst bytes.Buffer

	require.NoError(t, source.Fetch(t.Context(), artifacts[0], &dst))
	require.Equal(t, body, dst.String())

	require.NoError(t, os.Remove(filepath.Join(dir, tarball)))

	err = source.Fetch(t.Context(), artifacts[0], &dst)
	require.ErrorContains(t, err, "local bundle file not found")
}
