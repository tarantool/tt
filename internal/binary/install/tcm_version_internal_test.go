package install

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

func tcmVersionBundles(t *testing.T, names ...string) search.BundleInfoSlice {
	t.Helper()

	bundles := make(search.BundleInfoSlice, 0, len(names))
	for _, name := range names {
		ver, err := version.Parse(name)
		require.NoError(t, err)

		ver.Tarball = "tcm-" + name + ".linux.amd64.tar.gz"
		bundles = append(bundles, search.BundleInfo{
			Version: ver,
			Package: "tcm",
			Release: "release-" + name,
			Token:   "token-" + name,
		})
	}

	sort.Sort(bundles)

	return bundles
}

func TestSelectTcmBundleVersion(t *testing.T) {
	bundles := tcmVersionBundles(t,
		"1.3.0",
		"1.3.0-0-g3857712a",
		"1.3.0-2-g074b5ffa",
		"1.3.1-0-g074b5ffa",
		"1.3.2-rc1-0-g074b5ffa",
		"1.4.0-0-g074b5ffa",
		"1.5.0-rc1-0-g074b5ffa",
		"1.5.0-rc2-0-g074b5ffa",
		"2.0.0-0-g074b5ffa",
		"3.0.0-rc1-0-g074b5ffa",
	)

	cases := []struct {
		name      string
		requested string
		want      string
	}{
		{"latest", "", "2.0.0-0-g074b5ffa"},
		{"major", "1", "1.4.0-0-g074b5ffa"},
		{"minor", "1.3", "1.3.1-0-g074b5ffa"},
		{"patch", "1.3.0", "1.3.0-2-g074b5ffa"},
		{"full_old_build", "1.3.0-0-g3857712a", "1.3.0-0-g3857712a"},
		{"full_rc", "1.5.0-rc1-0-g074b5ffa", "1.5.0-rc1-0-g074b5ffa"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectBundleVersion(bundles, tc.requested, binary.ProgramTcm)
			require.NoError(t, err)

			want, err := search.SelectVersion(bundles, tc.want)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestSelectTcmBundleVersionRejectsMasks(t *testing.T) {
	bundles := tcmVersionBundles(t,
		"1.3.0-0-g3857712a",
		"1.4.0-rc1-0-g074b5ffa",
	)

	for _, requested := range []string{
		"1.3.0-0",
		"1.3.0-g3857712a",
		"1.4.0-rc1",
		"1.3.0-0-g074b5ffa",
		"v1.3.0",
		"latest",
		"3",
		"1.2",
		"1.3.1",
		"1.4",
		"1.4.0",
		"1.3.0.0",
	} {
		t.Run(requested, func(t *testing.T) {
			_, err := selectBundleVersion(bundles, requested, binary.ProgramTcm)
			require.Error(t, err)
		})
	}
}

func TestSelectTcmBundleVersionRequiresStable(t *testing.T) {
	bundles := tcmVersionBundles(t,
		"1.3.0-alpha1-0-g074b5ffa",
		"1.3.0-beta1-0-g074b5ffa",
		"1.3.0-rc1-0-g074b5ffa",
	)
	for _, requested := range []string{"", "1", "1.3", "1.3.0"} {
		t.Run(requested, func(t *testing.T) {
			_, err := selectBundleVersion(bundles, requested, binary.ProgramTcm)
			require.ErrorIs(t, err, version.ErrNotFound)
		})
	}
}

func TestSelectTcmBundleVersionEmpty(t *testing.T) {
	for _, requested := range []string{"", "1", "1.3", "1.3.0", "1.3.0-0-g3857712a"} {
		t.Run(requested, func(t *testing.T) {
			_, err := selectBundleVersion(nil, requested, binary.ProgramTcm)
			require.Error(t, err)
		})
	}
}

func TestSelectTtEeStandaloneVersion(t *testing.T) {
	// Version resolution is shared with TCM; neither uses SDK release numbers.
	bundles := tcmVersionBundles(t, "2.14.0", "2.15.0", "2.15.1", "2.16.0-rc1", "3.0.0")
	for _, testCase := range []struct {
		requested string
		want      string
	}{
		{"", "3.0.0"},
		{"2", "2.15.1"},
		{"2.15", "2.15.1"},
		{"2.15.0", "2.15.0"},
		{"2.16.0-rc1", "2.16.0-rc1"},
	} {
		t.Run(testCase.requested, func(t *testing.T) {
			selected, err := selectBundleVersion(bundles, testCase.requested, binary.ProgramTtEe)
			require.NoError(t, err)
			require.Equal(t, testCase.want, selected.Version.Str)
		})
	}
}
