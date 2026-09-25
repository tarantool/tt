package version

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// platform is what the full version says the build runs on.
const platform = runtime.GOOS + "/" + runtime.GOARCH

// setCoreBuild sets the values the build stamps the core's version with,
// for the duration of t.
func setCoreBuild(t *testing.T, tag, commit, commitsSinceTag, label string) {
	t.Helper()

	previous := []string{gitTag, gitCommit, gitCommitSinceTag, versionLabel}

	t.Cleanup(func() {
		gitTag, gitCommit, gitCommitSinceTag, versionLabel =
			previous[0], previous[1], previous[2], previous[3]
	})

	gitTag, gitCommit, gitCommitSinceTag, versionLabel = tag, commit, commitsSinceTag, label
}

// versionForms are the three forms of one version.
type versionForms struct {
	full, short, commit string
}

// assertForms checks the three forms getVersion renders.
func assertForms(t *testing.T, want versionForms, getVersion func(bool, bool) string) {
	t.Helper()

	assert.Equal(t, want.full, getVersion(false, false), "full")
	assert.Equal(t, want.short, getVersion(true, false), "short")
	assert.Equal(t, want.commit, getVersion(false, true), "commit")
	assert.Equal(t, want.commit, getVersion(true, true), "short with commit")
}

// TestGetVersion checks the core's version as the build stamps it.
//
//nolint:paralleltest // Replaces the values the build stamps.
func TestGetVersion(t *testing.T) {
	for _, testCase := range []struct {
		name                         string
		tag, commit, sinceTag, label string
		want                         versionForms
	}{
		{
			name: "release", tag: "v2.11.5", commit: "abc1234", sinceTag: "0",
			want: versionForms{
				full:   "Tarantool CLI version 2.11.5, " + platform + ". commit: abc1234",
				short:  "2.11.5",
				commit: "2.11.5.abc1234",
			},
		},
		{
			name: "past the tag", tag: "v2.11.5-34-gabc1234", commit: "abc1234", sinceTag: "34",
			want: versionForms{
				full: "Tarantool CLI version 2.11.5, " + platform +
					". commit: abc1234 (v2.11.5-34-gabc1234)",
				short:  "2.11.5",
				commit: "2.11.5.abc1234",
			},
		},
		{
			name: "label", tag: "v2.11.5", commit: "abc1234", label: "foo",
			want: versionForms{
				full:   "Tarantool CLI version 2.11.5/foo, " + platform + ". commit: abc1234",
				short:  "2.11.5/foo",
				commit: "2.11.5/foo.abc1234",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setCoreBuild(t, testCase.tag, testCase.commit, testCase.sinceTag, testCase.label)

			assertForms(t, testCase.want, GetVersion)
			assertForms(t, testCase.want, Flavour{}.GetVersion)
		})
	}
}

// TestFlavourGetVersion checks the version of a distribution that presents
// itself with a name, a version and an edition of its own.
//
//nolint:paralleltest // Replaces the values the build stamps.
func TestFlavourGetVersion(t *testing.T) {
	// The core's own version must not show through the distribution's.
	setCoreBuild(t, "v3.1.0", "0000000", "0", "")

	for _, testCase := range []struct {
		name    string
		flavour Flavour
		want    versionForms
	}{
		{
			name: "edition",
			flavour: Flavour{
				Title:   "Tarantool CLI EE",
				Version: &Info{Tag: "v2.15.0", Commit: "def5678", CommitsSinceTag: 0, Label: ""},
				Edition: "ee",
			},
			want: versionForms{
				full:   "Tarantool CLI EE version 2.15.0, " + platform + ". commit: def5678",
				short:  "2.15.0+ee",
				commit: "2.15.0+ee.def5678",
			},
		},
		{
			name: "past the tag",
			flavour: Flavour{
				Title: "Tarantool CLI EE",
				Version: &Info{
					Tag: "v2.15.0-3-gdef5678", Commit: "def5678", CommitsSinceTag: 3, Label: "",
				},
				Edition: "ee",
			},
			want: versionForms{
				full: "Tarantool CLI EE version 2.15.0, " + platform +
					". commit: def5678 (v2.15.0-3-gdef5678)",
				short:  "2.15.0+ee",
				commit: "2.15.0+ee.def5678",
			},
		},
		{
			name: "no edition",
			flavour: Flavour{
				Title:   "Custom CLI",
				Version: &Info{Tag: "v1.0.0", Commit: "1234567", CommitsSinceTag: 0, Label: ""},
				Edition: "",
			},
			want: versionForms{
				full:   "Custom CLI version 1.0.0, " + platform + ". commit: 1234567",
				short:  "1.0.0",
				commit: "1.0.0.1234567",
			},
		},
		{
			name: "unknown version",
			flavour: Flavour{
				Title: "Tarantool CLI EE", Version: &Info{}, Edition: "ee",
			},
			want: versionForms{
				full:   "Tarantool CLI EE version <unknown>, " + platform + ". commit: ",
				short:  "<unknown>+ee",
				commit: "<unknown>+ee.",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assertForms(t, testCase.want, testCase.flavour.GetVersion)
			assert.Equal(t, "3.1.0", GetVersion(true, false), "the core's own version")
		})
	}
}

// setBuildInfo makes the build info say the core is at the module version
// module, or, when ok is false, nothing about the core, for the duration of
// t.
func setBuildInfo(t *testing.T, module string, ok bool) {
	t.Helper()

	previous := readCoreVersion

	t.Cleanup(func() { readCoreVersion = previous })

	readCoreVersion = func() (string, bool) { return module, ok }
}

// TestGetVersionFromBuildInfo checks the core's version of a build that
// stamps no tag, taken from the module version in the build info.
//
//nolint:paralleltest // Replaces the values the build stamps and the build info.
func TestGetVersionFromBuildInfo(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		module string
		want   versionForms
	}{
		{
			name: "release", module: "v3.1.0",
			want: versionForms{
				full:   "Tarantool CLI version 3.1.0, " + platform + ". commit: <unknown>",
				short:  "3.1.0",
				commit: "3.1.0.<unknown>",
			},
		},
		{
			name: "modified release", module: "v3.1.0+dirty",
			want: versionForms{
				full: "Tarantool CLI version 3.1.0, " + platform +
					". commit: <unknown> (v3.1.0+dirty)",
				short:  "3.1.0",
				commit: "3.1.0.<unknown>",
			},
		},
		{
			name: "past a release", module: "v3.1.1-0.20260925120000-abcdef123456",
			want: versionForms{
				full: "Tarantool CLI version 3.1.0, " + platform +
					". commit: abcdef123456 (v3.1.1-0.20260925120000-abcdef123456)",
				short:  "3.1.0",
				commit: "3.1.0.abcdef123456",
			},
		},
		{
			name: "modified past a release", module: "v3.1.1-0.20260925120000-abcdef123456+dirty",
			want: versionForms{
				full: "Tarantool CLI version 3.1.0, " + platform +
					". commit: abcdef123456 (v3.1.1-0.20260925120000-abcdef123456+dirty)",
				short:  "3.1.0",
				commit: "3.1.0.abcdef123456",
			},
		},
		{
			name: "past a pre-release", module: "v3.2.0-rc1.0.20260925120000-abcdef123456",
			want: versionForms{
				full: "Tarantool CLI version 3.2.0, " + platform +
					". commit: abcdef123456 (v3.2.0-rc1.0.20260925120000-abcdef123456)",
				short:  "3.2.0",
				commit: "3.2.0.abcdef123456",
			},
		},
		{
			name: "no tag before", module: "v3.0.0-20260925120000-abcdef123456",
			want: versionForms{
				full: "Tarantool CLI version <unknown>, " + platform +
					". commit: abcdef123456 (v3.0.0-20260925120000-abcdef123456)",
				short:  "<unknown>",
				commit: "<unknown>.abcdef123456",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setCoreBuild(t, "", "", "", "")
			setBuildInfo(t, testCase.module, true)

			assertForms(t, testCase.want, GetVersion)
		})
	}
}

// TestGetVersionBuildInfoUnused checks that the build info does not stand in
// for a tag the build stamps, nor for a distribution's version.
//
//nolint:paralleltest // Replaces the values the build stamps and the build info.
func TestGetVersionBuildInfoUnused(t *testing.T) {
	setBuildInfo(t, "v9.9.9", true)

	t.Run("stamped tag", func(t *testing.T) {
		setCoreBuild(t, "v3.1.0", "abc1234", "0", "")

		assert.Equal(t, "3.1.0.abc1234", GetVersion(false, true))
	})

	t.Run("distribution", func(t *testing.T) {
		setCoreBuild(t, "", "", "", "")

		ee := Flavour{Title: "Tarantool CLI EE", Version: &Info{}, Edition: "ee"}
		assert.Equal(t, "<unknown>+ee", ee.GetVersion(true, false))
		assert.Equal(t, "9.9.9", GetVersion(true, false), "the core's own version")
	})

	t.Run("no build info", func(t *testing.T) {
		setCoreBuild(t, "", "abc1234", "", "")
		setBuildInfo(t, "", false)

		assertForms(t, versionForms{
			full:   "Tarantool CLI version <unknown>, " + platform + ". commit: abc1234",
			short:  "<unknown>",
			commit: "<unknown>.abc1234",
		}, GetVersion)
	})
}

// TestFlavourName checks the name a distribution presents itself under.
func TestFlavourName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Tarantool CLI", Flavour{}.Name())
	assert.False(t, Flavour{}.Custom())
	assert.False(t, Flavour{Title: "Tarantool CLI"}.Custom())
	assert.Equal(t, "Tarantool CLI EE", Flavour{Title: "Tarantool CLI EE"}.Name())
	assert.True(t, Flavour{Title: "Tarantool CLI EE"}.Custom())
}
