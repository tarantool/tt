package search

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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
			want: []BundleInfo{
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
			want: []BundleInfo{
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
			got, err := getBundles(tt.args.rawBundleInfoList, tt.args.flags)
			if (err != nil) != tt.wantErr {
				t.Errorf("getBundles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equalf(t, tt.want, got, "getBundles()")
		})
	}
}

func Test_GetCommitFromGitLocal(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		tarlink string
		hashes  []string
		wantErr bool
	}{
		{
			name:    "first repo",
			link:    "./testdata/empty_repo/",
			tarlink: "./testdata/test_repo.tar/",
			hashes:  []string{"c779d17", "6a05d6a", "6f05cd1"},
			wantErr: false,
		},
		{
			name:    "second repo",
			link:    "./testdata/empty_repo/",
			tarlink: "./testdata/test_repo.tar/",
			hashes:  []string{"111111"},
			wantErr: true,
		},
	}

	for _, repo := range tests {
		t.Run(repo.name, func(t *testing.T) {
			err := util.ExtractTar(repo.tarlink)
			defer os.RemoveAll(repo.link)
			if err != nil {
				t.Errorf("ExtractTar() error %v", err)
			}
			for _, hash := range repo.hashes {
				_, err := GetCommitFromGitLocal(repo.link, hash)
				if (err != nil) != repo.wantErr {
					t.Errorf("GetCommitsFromGitRemote() error = %v, wantErr %v", err, repo.wantErr)
					return
				}
			}
		})
	}
}

func Test_GetCommitFromGitRemote(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		tarlink string
		hashes  []string
		wantErr bool
	}{
		{
			name:    "first repo",
			link:    "./testdata/empty_repo/",
			tarlink: "./testdata/test_repo.tar/",
			hashes:  []string{"c779d17", "6a05d6a", "6f05cd1"},
			wantErr: false,
		},
		{
			name:    "second repo",
			link:    "./testdata/empty_repo/",
			tarlink: "./testdata/test_repo.tar/",
			hashes:  []string{"111111"},
			wantErr: true,
		},
	}

	for _, repo := range tests {
		t.Run(repo.name, func(t *testing.T) {
			err := util.ExtractTar(repo.tarlink)
			defer os.RemoveAll(repo.link)
			if err != nil {
				t.Errorf("ExtractTar() error %v", err)
			}
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
