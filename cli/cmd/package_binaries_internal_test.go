package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/runtimepin"
	"github.com/tarantool/tt/v3/internal/binary/mirror"
)

// TestAddBinaryNamesNoBinaries: a program the lock pins no version of, and
// one whose pin [platform] no longer allows, fail before anything is
// downloaded, with a message that names tt package resolve and
// --no-binaries, which skips the programs.
func TestAddBinaryNamesNoBinaries(t *testing.T) {
	platform := manifest.Platform{
		Tarantool: manifest.Constraint{Version: ">=3.1.0"},
		Tt:        manifest.Constraint{Version: ">=3.1.0"},
	}

	tests := []struct {
		name string
		lock *manifest.Lock
		want error
	}{
		{name: "not pinned", lock: &manifest.Lock{}, want: errRuntimeNotPinned},
		{name: "stale pin", lock: &manifest.Lock{Tt: "3.0.0"}, want: errStalePin},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()

			_, err := addBinary(t.Context(), dir, dir, platform, testCase.lock,
				runtimepin.ComponentTt)
			require.ErrorIs(t, err, testCase.want)
			assert.Contains(t, err.Error(), "run tt package resolve")
			assert.Contains(t, err.Error(), "--"+noBinariesFlag)
			assert.NoDirExists(t, filepath.Join(dir, mirror.DirName))
		})
	}
}
