package rocks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
)

// rows are the rows of a server for versions, a version ending in "!" yanked.
func rows(t *testing.T, versions ...string) []luarocks.VersionedRock {
	t.Helper()

	out := make([]luarocks.VersionedRock, 0, len(versions))

	for _, version := range versions {
		raw, yanked := strings.CutSuffix(version, "!")

		parsed, err := deps.ParseVersion(raw)
		require.NoError(t, err)

		out = append(out, luarocks.VersionedRock{Name: "stat", Version: parsed, Yanked: yanked})
	}

	return out
}

// TestPickNewestPassesOverYanked checks the rule a server's yanked versions
// are picked by: only an == constraint, which is how a lock pins a version,
// chooses one, and a rock whose only release is yanked is not answered with
// its development branch instead.
func TestPickNewestPassesOverYanked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		candidates []luarocks.VersionedRock
		constraint string
		want       string // Empty: nothing is picked.
		yanked     bool   // The failure says the matching versions are yanked.
	}{
		{
			name:       "the newest release is yanked",
			candidates: rows(t, "1.0.0-1", "2.0.0-1!"),
			constraint: ">=1.0.0",
			want:       "1.0.0-1",
		},
		{
			name:       "a pin chooses a yanked version",
			candidates: rows(t, "1.0.0-1", "2.0.0-1!"),
			constraint: ">=1.0.0,==2.0.0-1",
			want:       "2.0.0-1",
		},
		{
			name:       "only yanked versions match",
			candidates: rows(t, "1.0.0-1", "2.0.0-1!"),
			constraint: ">=2.0.0",
			yanked:     true,
		},
		{
			name:       "the only release is yanked",
			candidates: rows(t, "1.0.0-1!", "scm-1"),
			constraint: "",
			yanked:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			constraints, err := deps.ParseConstraints(test.constraint)
			require.NoError(t, err)

			picked, ok := pickNewest(test.candidates, constraints)
			if test.want == "" {
				assert.False(t, ok, picked.Version.Raw)
			} else {
				require.True(t, ok)
				assert.Equal(t, test.want, picked.Version.Raw)
			}

			assert.Equal(t, test.yanked, yankedNote(test.candidates, constraints) != "" && !ok)
		})
	}
}
