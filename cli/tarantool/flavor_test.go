package tarantool_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestFlavorFromBanner(t *testing.T) {
	tests := []struct {
		name   string
		banner string
		want   string
	}{
		{"community", "Tarantool 3.2.0-0-g19607a903\nTarget: Darwin\n", tarantool.FlavorCE},
		{
			"enterprise", "Tarantool Enterprise 3.2.0-0-g19607a903\nTarget: Linux\n",
			tarantool.FlavorEE,
		},
		{"entrypoint community", "Tarantool 3.8.0-entrypoint-49-g97a3b38040\n", tarantool.FlavorCE},
		{"unrecognized banner", "weird\n", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tarantool.FlavorFromBanner(tt.banner))
		})
	}
}

// fakeTarantool writes an executable that prints banner for --version and
// returns its path.
func fakeTarantool(t *testing.T, banner string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tarantool")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho '"+banner+"'\n"), 0o755))

	return path
}

// TestExecutableFlavor pins that the flavor of a tarantool comes from the
// product tt binary installed it as, without running it, and from its
// version banner otherwise.
func TestExecutableFlavor(t *testing.T) {
	enterprise := fakeTarantool(t, "Tarantool Enterprise 3.8.1-0-g1a2b3c4-r1")
	community := fakeTarantool(t, "Tarantool 3.8.1-0-ge643f88a5e0c")

	cases := []struct {
		name string
		exe  tarantool.Executable
		want string
	}{
		{
			name: "installed Community Edition",
			exe:  tarantool.Executable{Path: enterprise, Program: binary.ProgramCe},
			want: tarantool.FlavorCE,
		},
		{
			name: "installed Enterprise Edition",
			exe:  tarantool.Executable{Path: community, Program: binary.ProgramEe},
			want: tarantool.FlavorEE,
		},
		{
			name: "Enterprise Edition from PATH",
			exe:  tarantool.Executable{Path: enterprise, Program: binary.ProgramUnknown},
			want: tarantool.FlavorEE,
		},
		{
			name: "Community Edition from PATH",
			exe:  tarantool.Executable{Path: community, Program: binary.ProgramUnknown},
			want: tarantool.FlavorCE,
		},
		{
			name: "a tarantool that does not run",
			exe: tarantool.Executable{
				Path: filepath.Join(t.TempDir(), "missing"), Program: binary.ProgramUnknown,
			},
			want: "",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.exe.Flavor(t.Context()))
		})
	}
}
