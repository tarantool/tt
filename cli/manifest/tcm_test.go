package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

func TestSetPlatformTCM(t *testing.T) {
	t.Run("adds the key after the last one of [platform]", func(t *testing.T) {
		src := editHeader + "\n[dependencies]\nchecks = \"3.3.0\"\n"

		editor, err := manifest.NewEditor([]byte(src))
		require.NoError(t, err)

		change, err := editor.SetPlatformTCM(">=1.3")
		require.NoError(t, err)
		assert.False(t, change.Existed)

		edited := editor.Bytes()
		assert.Equal(t, editPrelude+`
[package]
name = "demo"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"
tcm = ">=1.3"

[dependencies]
checks = "3.3.0"
`, string(edited))

		assert.Equal(t, ">=1.3", reparse(t, edited).Platform.Tcm.Version)
	})

	t.Run("replaces the value in place", func(t *testing.T) {
		src := editHeader + "tcm = \">=1.2\" # pinned for the demo\n"

		editor, err := manifest.NewEditor([]byte(src))
		require.NoError(t, err)

		change, err := editor.SetPlatformTCM(">=1.3,<2")
		require.NoError(t, err)
		assert.True(t, change.Existed)
		assert.Equal(t, ">=1.2", change.Previous)
		assert.Equal(t, editHeader+"tcm = \">=1.3,<2\" # pinned for the demo\n",
			string(editor.Bytes()))
	})

	t.Run("refuses a value that is not a string", func(t *testing.T) {
		editor, err := manifest.NewEditor([]byte(editHeader + "tcm = 1\n"))
		require.NoError(t, err)

		_, err = editor.SetPlatformTCM(">=1.3")
		require.ErrorIs(t, err, manifest.ErrUnsupportedForm)
	})
}

func TestCheckTCM(t *testing.T) {
	cases := []struct {
		name      string
		tarantool string
		want      string
	}{
		{
			name:      "no flavor",
			tarantool: ">=3.0.0",
			want: `TCM is Tarantool Enterprise only, and [platform].tarantool of ` +
				`app.manifest.toml is ">=3.0.0", with no flavor, which is [ce]: Tarantool ` +
				`Community Edition; switch the project to Tarantool Enterprise, ` +
				`tarantool = ">=3.0.0[ee]", to run TCM`,
		},
		{
			name:      "community",
			tarantool: ">=3.2,<4[ce]",
			want: `TCM is Tarantool Enterprise only, and [platform].tarantool of ` +
				`app.manifest.toml is ">=3.2,<4[ce]", with the flavor [ce]: Tarantool ` +
				`Community Edition; switch the project to Tarantool Enterprise, ` +
				`tarantool = ">=3.2,<4[ee]", to run TCM`,
		},
		{name: "enterprise", tarantool: ">=3.0.0[ee]"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var constraint manifest.Constraint

			require.NoError(t, constraint.UnmarshalText([]byte(test.tarantool)))

			err := manifest.CheckTCM(manifest.Platform{Tarantool: constraint})
			if test.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, manifest.ErrTCMCommunity)
			assert.EqualError(t, err, test.want)
		})
	}
}
