package sdk

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoreVersion(t *testing.T) {
	t.Parallel()

	other := &debug.Module{Path: "github.com/spf13/cobra", Version: "v1.10.2"}

	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
		ok   bool
	}{
		{
			name: "no build info",
			info: nil,
		},
		{
			name: "tt itself",
			info: &debug.BuildInfo{Main: debug.Module{Path: corePath, Version: "v3.1.0"}},
			want: "v3.1.0",
			ok:   true,
		},
		{
			name: "tt from a working tree",
			info: &debug.BuildInfo{Main: debug.Module{Path: corePath, Version: "(devel)"}},
		},
		{
			name: "a distribution requiring the core",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/tt-dist", Version: "(devel)"},
				Deps: []*debug.Module{other, {Path: corePath, Version: "v3.2.0"}},
			},
			want: "v3.2.0",
			ok:   true,
		},
		{
			name: "a distribution replacing the core with a version",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/tt-dist"},
				Deps: []*debug.Module{{
					Path: corePath, Version: "v3.2.0",
					Replace: &debug.Module{Path: "example.com/fork/tt/v3", Version: "v3.2.1"},
				}},
			},
			want: "v3.2.1",
			ok:   true,
		},
		{
			name: "a distribution replacing the core with a directory",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/tt-dist"},
				Deps: []*debug.Module{{
					Path: corePath, Version: "v3.2.0",
					Replace: &debug.Module{Path: "../tt"},
				}},
			},
		},
		{
			name: "a binary without the core",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/other", Version: "v1.0.0"},
				Deps: []*debug.Module{other},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, ok := coreVersion(func() (*debug.BuildInfo, bool) {
				return testCase.info, testCase.info != nil
			})

			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.ok, ok)
		})
	}
}

// TestCoreVersionOfTestBinary reads the real build info: a test binary of
// this module has no core in it.
func TestCoreVersionOfTestBinary(t *testing.T) {
	t.Parallel()

	got, ok := CoreVersion()

	assert.False(t, ok)
	assert.Empty(t, got)
}
