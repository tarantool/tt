package rocks

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
)

func TestAddLuarocksRepoOpts(t *testing.T) {
	type args struct {
		cliOpts *config.CliOpts
		args    []string
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{
			"Rocks repo is not specified",
			args{
				&config.CliOpts{
					Repo: &config.RepoOpts{
						Rocks: "",
					},
				},
				[]string{},
			},
			[]string{},
			false,
		},
		{
			"Nil repo config",
			args{
				&config.CliOpts{
					Repo: nil,
				},
				[]string{},
			},
			[]string{},
			false,
		},
		{
			"Rock repo is specified",
			args{
				&config.CliOpts{
					Repo: &config.RepoOpts{
						Rocks: "local_path",
					},
				},
				[]string{},
			},
			[]string{"--server", "local_path"},
			false,
		},
		{
			"Rock repo is specified and --only-server opt is provided",
			args{
				&config.CliOpts{
					Repo: &config.RepoOpts{
						Rocks: "local_path",
					},
				},
				[]string{"--only-server", "/other/repo"},
			},
			[]string{"--only-server", "/other/repo"}, // No --server option is added.
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := addLuarocksRepoOpts(tt.args.cliOpts, tt.args.args)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.EqualValues(t, got, tt.want)
		})
	}
}
