package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
)

func Test_generateRunDirForCartridge(t *testing.T) {
	testDataAbs, err := filepath.Abs("testdata")
	require.NoError(t, err)
	type args struct {
		env       config.TtEnvOpts
		configDir string
		runDir    string
		appName   string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "Absolute run dir",
			args: args{
				env: config.TtEnvOpts{
					InstancesEnabled: "/etc/tarantool/instance_enable",
				},
				configDir: "./testdata",
				runDir:    "/var/run/tarantool",
				appName:   "app1",
			},
			want:    "/var/run/tarantool/app1",
			wantErr: false,
		},
		{
			name: "Relative run dir",
			args: args{
				env: config.TtEnvOpts{
					InstancesEnabled: "/etc/tarantool/instance_enable",
				},
				configDir: "./testdata",
				runDir:    "var/run/",
				appName:   "app1",
			},
			want:    "/etc/tarantool/instance_enable/app1/var/run",
			wantErr: false,
		},
		{
			name: "Relative run dir, instance enabled is ., no app in cfg dir",
			args: args{
				env: config.TtEnvOpts{
					InstancesEnabled: ".",
				},
				configDir: "./testdata",
				runDir:    "var/run/",
				appName:   "app1",
			},
			want:    testDataAbs + "/app1/var/run",
			wantErr: false,
		},
		{
			name: "Relative run dir, instance enabled is ., app in cfg dir",
			args: args{
				env: config.TtEnvOpts{
					InstancesEnabled: ".",
				},
				configDir: "./testdata/with_init",
				runDir:    "var/run/",
				appName:   "app1",
			},
			want:    testDataAbs + "/with_init/var/run",
			wantErr: false,
		},
		{
			name: "Absolute run dir, no app name",
			args: args{
				env: config.TtEnvOpts{
					InstancesEnabled: ".",
				},
				configDir: "./testdata",
				runDir:    "/var/run/",
				appName:   "",
			},
			want:    "/var/run",
			wantErr: false,
		},
		{
			name: "Relative run dir, no app name",
			args: args{
				env: config.TtEnvOpts{
					InstancesEnabled: ".",
				},
				configDir: "./testdata",
				runDir:    "",
				appName:   "",
			},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generateRunDirForCartridge(tt.args.env, tt.args.configDir, tt.args.runDir,
				tt.args.appName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
