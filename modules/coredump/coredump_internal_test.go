package coredump

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// TestPackTarantool checks the executable pack packs a core with: the one
// --executable names, else the Tarantool tt runs, else none, for the pack
// script to look up in PATH.
func TestPackTarantool(t *testing.T) {
	t.Parallel()

	version := sdk.TarantoolVersion{Major: 3, Minor: 9, Patch: 0}

	cases := []struct {
		name       string
		executable string
		opts       []sdktest.Option
		want       string
	}{
		{
			name:       "flag",
			executable: "/opt/tarantool",
			opts:       []sdktest.Option{sdktest.WithTarantool("/usr/bin/tarantool", version)},
			want:       "/opt/tarantool",
		},
		{
			name:       "services",
			executable: "",
			opts: []sdktest.Option{
				sdktest.WithTarantool("/app/.tarantool/bin/tarantool", version),
			},
			want: "/app/.tarantool/bin/tarantool",
		},
		{name: "none", executable: "", opts: nil, want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			services := sdktest.New(t, testCase.opts...)
			services.Start()

			pack := &packCmd{
				services: services, executable: testCase.executable, directory: "", time: "",
				pid: 0,
			}

			got, err := pack.tarantool()
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

// failingServices are Services whose Tarantool fails with another error than
// a Tarantool not found.
type failingServices struct {
	sdk.Services
}

// errBroken is the error of failingServices.Tarantool.
var errBroken = errors.New("broken TT_USE_SYSTEM_TARANTOOL")

// Tarantool fails with errBroken.
func (failingServices) Tarantool() (sdk.Tarantool, error) {
	return nil, errBroken
}

func TestPackTarantoolError(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	pack := &packCmd{
		services: failingServices{Services: services}, executable: "", directory: "", time: "",
		pid: 0,
	}

	_, err := pack.tarantool()
	require.ErrorIs(t, err, errBroken)
	assert.ErrorContains(t, err, "cannot find the tarantool executable")
}
