package version

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

// sdkRelease is the SDK's name for each release type.
var sdkRelease = map[ReleaseType]sdk.ReleaseType{
	TypeNightly: sdk.ReleaseNightly,
	TypeAlpha:   sdk.ReleaseAlpha,
	TypeBeta:    sdk.ReleaseBeta,
	TypeRC:      sdk.ReleaseRC,
	TypeRelease: sdk.ReleaseGA,
}

// TestParseAgreesWithSDK checks that the SDK reads every Tarantool version
// the way Parse does: modules see the version through the SDK, the core
// through Parse, and the two must not disagree about the same executable.
func TestParseAgreesWithSDK(t *testing.T) {
	t.Parallel()

	cases := parseVersionCases()
	require.NotEmpty(t, cases)

	for input := range cases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			want, wantErr := Parse(input)
			got, err := sdk.ParseTarantoolVersion(input)

			if wantErr != nil {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, sdk.TarantoolVersion{
				Major:      want.Major,
				Minor:      want.Minor,
				Patch:      want.Patch,
				Release:    sdkRelease[want.Release.Type],
				ReleaseNum: want.Release.Num,
				Commits:    want.Additional,
				Hash:       strings.TrimPrefix(want.Hash, "g"),
				Revision:   want.Revision,
				BuildName:  want.BuildName,
				Edition:    sdk.EditionUnknown,
				Raw:        want.Str,
			}, got)
			assert.Equal(t, want.Str, got.String())
		})
	}
}
