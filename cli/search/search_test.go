package search

import (
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

func Test_getBundles(t *testing.T) {
	pkgRelease := "tarantool-enterprise-sdk-nogc64-2.10.6-0-r549.linux.x86_64.tar.gz"
	pkgDebug := "tarantool-enterprise-sdk-debug-nogc64-2.10.6-0-r549.linux.x86_64.tar.gz"

	type args struct {
		rawBundleInfoList map[string][]string
		flags             SearchFlags
	}
	tests := []struct {
		name    string
		args    args
		want    BundleInfoSlice
		wantErr bool
	}{
		{
			name: "Random data",
			args: args{
				map[string][]string{"random": {"data"}},
				SearchRelease,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Search release (OK)",
			args: args{
				map[string][]string{"2.10": {pkgRelease}},
				SearchRelease,
			},
			want: BundleInfoSlice{
				{
					Version: version.Version{
						Major:      2,
						Minor:      10,
						Patch:      6,
						Additional: 0,
						Revision:   549,
						Release:    version.Release{Type: version.TypeRelease},
						Hash:       "",
						Str:        "nogc64-2.10.6-0-r549",
						Tarball:    pkgRelease,
						BuildName:  "nogc64",
					},
					Release: "2.10",
					Package: "enterprise",
				},
			},
			wantErr: false,
		},
		{
			name: "Search debug (OK)",
			args: args{
				map[string][]string{"2.10": {pkgDebug}},
				SearchDebug,
			},
			want: BundleInfoSlice{
				{
					Version: version.Version{
						Major:      2,
						Minor:      10,
						Patch:      6,
						Additional: 0,
						Revision:   549,
						Release:    version.Release{Type: version.TypeRelease},
						Hash:       "",
						Str:        "debug-nogc64-2.10.6-0-r549",
						Tarball:    pkgDebug,
						BuildName:  "debug-nogc64",
					},
					Release: "2.10",
					Package: "enterprise",
				},
			},
			wantErr: false,
		},
		{
			name: "Search release (Err)",
			args: args{
				map[string][]string{"2.10": {pkgDebug}},
				SearchRelease,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Search debug (Err)",
			args: args{
				map[string][]string{"2.10": {pkgRelease}},
				SearchDebug,
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sCtx := SearchCtx{
				Program: ProgramEe,
				Package: "enterprise",
				Filter:  tt.args.flags,
			}
			got, err := getBundles(tt.args.rawBundleInfoList, &sCtx)
			if (err != nil) != tt.wantErr {
				t.Errorf("getBundles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equalf(t, tt.want, got, "getBundles()")
		})
	}
}

func Test_GetCommitFromGitLocal(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test_repo.tar")
	require.NoError(t, copy.Copy("./testdata/test_repo.tar", tarPath))
	extractedRepoPath := filepath.Join(tmpDir, "empty_repo")

	tests := []struct {
		name    string
		link    string
		tarlink string
		hashes  []string
		wantErr bool
	}{
		{
			name:    "hash should be found",
			link:    extractedRepoPath,
			tarlink: tarPath,
			hashes:  []string{"c779d17", "6a05d6a", "6f05cd1"},
			wantErr: false,
		},
		{
			name:    "missing hash",
			link:    extractedRepoPath,
			tarlink: tarPath,
			hashes:  []string{"111111"},
			wantErr: true,
		},
	}

	for _, repo := range tests {
		t.Run(repo.name, func(t *testing.T) {
			require.NoError(t, util.ExtractTar(repo.tarlink))
			for _, hash := range repo.hashes {
				_, err := GetCommitFromGitLocal(repo.link, hash)
				if (err != nil) != repo.wantErr {
					t.Errorf("GetCommitsFromGitLocal() error = %v, wantErr %v", err, repo.wantErr)
					return
				}
			}
		})
	}
}

func Test_GetCommitFromGitRemote(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test_repo.tar")
	require.NoError(t, copy.Copy("./testdata/test_repo.tar", tarPath))
	extractedRepoPath := filepath.Join(tmpDir, "empty_repo")

	tests := []struct {
		name    string
		link    string
		tarlink string
		hashes  []string
		wantErr bool
	}{
		{
			name:    "hash should be found",
			link:    extractedRepoPath,
			tarlink: tarPath,
			hashes:  []string{"c779d17", "6a05d6a", "6f05cd1"},
			wantErr: false,
		},
		{
			name:    "missing hash",
			link:    extractedRepoPath,
			tarlink: tarPath,
			hashes:  []string{"111111"},
			wantErr: true,
		},
	}

	for _, repo := range tests {
		t.Run(repo.name, func(t *testing.T) {
			require.NoError(t, util.ExtractTar(repo.tarlink))
			for _, hash := range repo.hashes {
				_, err := GetCommitFromGitRemote(repo.link, hash)
				if (err != nil) != repo.wantErr {
					t.Errorf("GetCommitsFromGitRemote() error = %v, wantErr %v", err, repo.wantErr)
					return
				}
			}
		})
	}
}

func TestNewSearchCtx(t *testing.T) {
	t.Run("Valid context", func(t *testing.T) {
		got := NewSearchCtx(NewPlatformInformer(), NewTntIoDoer())
		require.NotNil(t, got)
		assert.Equal(t, got.Filter, SearchRelease)
		assert.Equal(t, got.Program, ProgramUnknown)
		assert.Equal(t, got.Package, "")
		assert.Equal(t, got.ReleaseVersion, "")
		assert.Equal(t, got.DevBuilds, false)
		assert.Implements(t, (*PlatformInformer)(nil), got.platformInformer)
		assert.Implements(t, (*TntIoDoer)(nil), got.TntIoDoer)
	})
}
