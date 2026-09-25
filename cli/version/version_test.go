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
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			setCoreBuild(t, tc.tag, tc.commit, tc.sinceTag, tc.label)

			assertForms(t, tc.want, GetVersion)
			assertForms(t, tc.want, Flavour{}.GetVersion)
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

	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			assertForms(t, tc.want, tc.flavour.GetVersion)
			assert.Equal(t, "3.1.0", GetVersion(true, false), "the core's own version")
		})
	}
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
