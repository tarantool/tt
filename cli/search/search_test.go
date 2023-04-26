package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
