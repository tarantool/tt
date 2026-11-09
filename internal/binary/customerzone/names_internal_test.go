package customerzone

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestParseArchive(t *testing.T) {
	pkgRelease := "tarantool-enterprise-sdk-nogc64-3.4.1-0-r63.linux.x86_64.tar.gz"
	pkgDebug := "tarantool-enterprise-sdk-debug-nogc64-3.4.1-0-r63.linux.x86_64.tar.gz"
	pkgOld := "tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz"

	tests := map[string]struct {
		program binary.Program
		name    string
		want    bundle.Artifact
		notOne  bool
		errMsg  string
	}{
		"random data": {
			program: binary.ProgramEe,
			name:    "data",
			notOne:  true,
		},
		"checksum": {
			program: binary.ProgramEe,
			name:    pkgRelease + ".sha256",
			notOne:  true,
		},
		"another product": {
			program: binary.ProgramTcm,
			name:    pkgRelease,
			notOne:  true,
		},
		"release": {
			program: binary.ProgramEe,
			name:    pkgRelease,
			want: bundle.Artifact{
				Version: "3.4.1-0-r63",
				Flavor:  []string{"nogc64"},
				Name:    pkgRelease,
			},
		},
		"debug": {
			program: binary.ProgramSDK,
			name:    pkgDebug,
			want: bundle.Artifact{
				Version: "3.4.1-0-r63",
				Flavor:  []string{"debug", "nogc64"},
				Name:    pkgDebug,
			},
		},
		"no flavour": {
			program: binary.ProgramEe,
			name:    pkgOld,
			want:    bundle.Artifact{Version: "2.8.4-0-r680", Name: pkgOld},
		},
		"tcm": {
			program: binary.ProgramTcm,
			name:    "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
			want: bundle.Artifact{
				Version: "1.3.1-0-g074b5ffa",
				Name:    "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
			},
		},
		"tt-ee": {
			program: binary.ProgramTtEe,
			name:    "tt-ee-2.15.0-linux-amd64.tar.gz",
			want: bundle.Artifact{
				Version: "2.15.0",
				Name:    "tt-ee-2.15.0-linux-amd64.tar.gz",
			},
		},
		"invalid sdk version": {
			program: binary.ProgramEe,
			name:    "tarantool-enterprise-sdk-gc64-Ver3.1.linux.x86_64.tar.gz",
			errMsg:  "parse the version of tarantool-enterprise-sdk-gc64-Ver3.1",
		},
		"invalid tcm version": {
			program: binary.ProgramTcm,
			name:    "tcm-1.3.1-X-g074b5ffa.linux.amd64.tar.gz",
			errMsg:  "parse the version of tcm-1.3.1-X-g074b5ffa",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok, err := parseArchive(tt.program, tt.name)
			if tt.errMsg != "" {
				require.True(t, ok, "the name is one of an archive")
				require.ErrorContains(t, err, tt.errMsg)

				return
			}

			require.NoError(t, err)

			if tt.notOne {
				require.False(t, ok)

				return
			}

			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestPlatformSuffix(t *testing.T) {
	tests := map[string]struct {
		program      binary.Program
		goos, goarch string
		expected     string
		errMsg       string
	}{
		"tt-ee linux amd64": {
			program:  binary.ProgramTtEe,
			goos:     "linux",
			goarch:   "amd64",
			expected: "-linux-amd64.tar.gz",
		},
		"tt-ee macos unsupported": {
			program: binary.ProgramTtEe,
			goos:    "darwin",
			goarch:  "arm64",
			errMsg: "unsupported OS: darwin: " +
				"tt-ee archives are available only for Linux amd64",
		},
		"tt-ee arm linux unsupported": {
			program: binary.ProgramTtEe,
			goos:    "linux",
			goarch:  "arm64",
			errMsg: "unsupported architecture: arm64: " +
				"tt-ee archives are available only for Linux amd64",
		},
		"tcm is listed by platform": {
			program: binary.ProgramTcm,
			goos:    "darwin",
			goarch:  "arm64",
		},
		"sdk is listed by platform": {
			program: binary.ProgramSDK,
			goos:    "linux",
			goarch:  "amd64",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := platformSuffix(tt.program, tt.goos, tt.goarch)
			if tt.errMsg != "" {
				require.EqualError(t, err, tt.errMsg)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestOnReleaseLine(t *testing.T) {
	parse := func(str string) version.Version {
		t.Helper()

		parsed, err := version.Parse(str)
		require.NoError(t, err)

		return parsed
	}

	require.True(t, onReleaseLine("", parse("2.14.0")))
	require.True(t, onReleaseLine("2", parse("2.14.0")))
	require.True(t, onReleaseLine("2.15", parse("2.15.1")))
	require.True(t, onReleaseLine("2.15.0", parse("2.15.0")))
	require.False(t, onReleaseLine("2.15", parse("2.14.0")))
	require.False(t, onReleaseLine("3", parse("2.15.0")))
}

func TestProducts(t *testing.T) {
	expected := []string{"tarantool-ee", "tarantool-sdk", "tcm", "tt-ee"}

	require.Equal(t, expected, newSource(nil).Products())
	require.Equal(t, expected, NewDir("").Products())
}
