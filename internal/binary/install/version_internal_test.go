package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestDefaultInstallTag(t *testing.T) {
	for _, test := range []struct {
		name    string
		program binary.Program
		tags    []string
		want    string
		wantErr error
	}{
		{
			name: "CE chooses the latest stable release", program: binary.ProgramCe,
			tags: []string{"3.8.0", "3.8.1", "4.0.0-rc1"}, want: "3.8.1",
		},
		{
			name:    "TT chooses the latest stable tt 3 and preserves tag spelling",
			program: binary.ProgramTt,
			tags:    []string{"v2.16.0", "v3.0.0", "v3.1.0", "v3.2.0-rc1", "v4.0.0"},
			want:    "v3.1.0",
		},
		{
			name: "TT takes a tt 3 prerelease while tt 3 has no release", program: binary.ProgramTt,
			tags: []string{"v2.16.0", "v3.0.0-rc1", "v3.0.0-rc2", "v4.0.0"}, want: "v3.0.0-rc2",
		},
		{
			name: "TT never takes tt 2", program: binary.ProgramTt,
			tags: []string{"v2.15.0", "v2.16.0"}, wantErr: errNoVersionsWereFetched,
		},
		{
			name: "CE needs a stable release", program: binary.ProgramCe,
			tags: []string{"4.0.0-rc1"}, wantErr: errNoVersionFound,
		},
		{name: "CE empty catalog", program: binary.ProgramCe, wantErr: errNoVersionFound},
		{name: "TT empty catalog", program: binary.ProgramTt, wantErr: errNoVersionsWereFetched},
		{
			name: "unsupported product", program: binary.ProgramTcm,
			wantErr: binary.ErrUnknownApplication,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var versions []version.Version

			for _, tag := range test.tags {
				parsed, err := version.Parse(tag)
				require.NoError(t, err)

				versions = append(versions, parsed)
			}

			actual, err := defaultInstallTag(test.program, versions)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, test.want, actual)
		})
	}
}

func TestResolveInstallVersionMasterWithoutRepository(t *testing.T) {
	t.Setenv("PATH", "")

	for _, program := range []binary.Program{binary.ProgramTt, binary.ProgramCe} {
		installCtx := InstallCtx{
			Program:          program,
			LocalRepo:        "missing",
			requestedVersion: masterVersion,
		}

		resolved, err := resolveInstallVersion(installCtx)
		require.NoError(t, err)
		assert.Equal(t, versionKindMaster, resolved.kind)
		assert.Equal(t, masterVersion, resolved.ref)
		assert.Equal(t, masterVersion, resolved.name)
	}
}
