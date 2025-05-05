package search_test

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/apex/log"
	"github.com/apex/log/handlers/memory"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/version"
)

// mockDirEntry is a mock implementation of fs.DirEntry for testing.
type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() fs.FileMode          { return 0 }
func (m mockDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

// mockFS is a mock implementation of fs.FS for testing.
type mockFS struct {
	entries []fs.DirEntry
}

// ReadDir implements interface [fs.ReadDirFS] and returns the entries in the directory.
// If the entries is empty, it returns an error [fs.ErrNotExist].
// If the entries is not readable, it returns an error [fs.ErrPermission].
func (m mockFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if m.entries == nil {
		return nil, fs.ErrPermission
	}
	if len(m.entries) == 0 {
		return nil, fs.ErrNotExist
	}
	return m.entries, nil
}

// Open returns a dummy file for compatibility with interface [fs.FS].
func (m mockFS) Open(name string) (fs.File, error) {
	return nil, errors.New("not implemented")
}

func TestFindLocalBundles(t *testing.T) {
	tests := map[string]struct {
		program         search.Program
		files           []fs.DirEntry
		expectedVersion version.VersionSlice
		errMsg          string
		logMsg          string
	}{
		"No matching files": {
			program: search.ProgramEe,
			files: []fs.DirEntry{
				mockDirEntry{name: "random-file.txt"},
				mockDirEntry{name: "another-file.log"},
			},
			// TODO: Проверять запись в логе:
			logMsg: fmt.Sprintf("No local SDK files found for %q", search.ProgramEe),
		},
		"No permission to read directory": {
			program: search.ProgramEe,
			errMsg:  "failed to read directory",
		},
		"Not exists directory": {
			program: search.ProgramEe,
			files:   []fs.DirEntry{},
			// TODO: Проверять запись в логе:
			logMsg: "Directory not found, cannot search for local SDK files",
		},
		"Matching files for " + search.ProgramEe.String(): {
			program: search.ProgramEe,
			files: []fs.DirEntry{
				mockDirEntry{
					name: "tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r5.linux.x86_64.tar.gz",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz.sha256",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r5.linux.x86_64" +
						".tar.gz.sha256",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz.sha256",
				},
				mockDirEntry{
					name: "tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz.sha256",
				},
			},
			expectedVersion: version.VersionSlice{
				{
					Tarball:  "tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz",
					Major:    2,
					Minor:    8,
					Patch:    4,
					Revision: 680,
					Release:  version.Release{Type: version.TypeRelease},
					Str:      "2.8.4-0-r680",
				},
				{
					Tarball:   "tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz",
					Major:     2,
					Minor:     8,
					Patch:     4,
					Revision:  680,
					Release:   version.Release{Type: version.TypeRelease},
					Str:       "debug-2.8.4-0-r680",
					BuildName: "debug",
				},
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
			program: search.ProgramEe,
			files: []fs.DirEntry{
				mockDirEntry{name: "tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz", isDir: true},
				mockDirEntry{name: "tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz"},
				mockDirEntry{
					name:  "tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz",
					isDir: true,
				},
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
		"Matching files for " + search.ProgramTcm.String(): {
			program: search.ProgramTcm,
			files: []fs.DirEntry{
				mockDirEntry{name: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz"},
				mockDirEntry{name: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz.sha256"},
				mockDirEntry{name: "tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz"},
				mockDirEntry{name: "tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz.sha256"},
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
			program: search.ProgramUnknown,
			files: []fs.DirEntry{
				mockDirEntry{name: "unsupported-program-1.0.0.tar.gz"},
			},
			errMsg: `local SDK file search not supported for "unknown(0)"`,
		},
		"Invalid version format for " + search.ProgramEe.String(): {
			program: search.ProgramEe,
			files: []fs.DirEntry{
				mockDirEntry{name: "tarantool-enterprise-sdk-gc64-Ver3.1.linux.x86_64.tar.gz"},
			},
			errMsg: "failed to parse version from file",
		},
		"Invalid version format for " + search.ProgramTcm.String(): {
			program: search.ProgramTcm,
			files: []fs.DirEntry{
				mockDirEntry{name: "tcm-1.3.1-X-g074b5ffa.linux.amd64.tar.gz"},
			},
			errMsg: "failed to parse version from file",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			handler := memory.New()
			log.SetHandler(handler)
			log.SetLevel(log.DebugLevel)

			mockFS := mockFS{entries: tt.files}
			bundles, err := search.FindLocalBundles(tt.program, &mockFS)

			if tt.errMsg != "" {
				if err == nil {
					t.Fatalf("expected an error but got none")
				}
				require.ErrorContains(t, err, tt.errMsg)
				return
			}
			require.NoError(t, err)

			require.Equal(t, len(tt.expectedVersion), len(bundles))

			for i, bundle := range bundles {
				require.Equal(t, tt.expectedVersion[i], bundle.Version, "index %d", i)
			}

			if tt.logMsg != "" {
				found := false
				for _, entry := range handler.Entries {
					if strings.Contains(entry.Message, tt.logMsg) {
						found = true
						break
					}
				}
				require.True(t, found, "expected %q not found in log entries", tt.logMsg)
			}
		})
	}
}
