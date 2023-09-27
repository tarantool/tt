package cfg

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
)

func getCliOpts(t *testing.T, configFile string) *config.CliOpts {
	cliOpts, configPath, err := configure.GetCliOpts(configFile)
	require.NoError(t, err)
	require.Equal(t, configFile, configPath)
	return cliOpts
}

func TestRunDump(t *testing.T) {
	type args struct {
		cmdCtx  *cmdcontext.CmdCtx
		dumpCtx *DumpCtx
		cliOpts *config.CliOpts
	}

	cwd, err := os.Getwd()
	require.NoError(t, err)
	configDir := filepath.Join(cwd, "testdata")

	tests := []struct {
		name       string
		args       args
		wantWriter string
		wantErr    bool
	}{
		{
			name: "Raw dump of configuration file",
			args: args{
				&cmdcontext.CmdCtx{
					Cli: cmdcontext.CliCtx{
						ConfigPath: "./testdata/tt_cfg.yaml",
					},
				},
				&DumpCtx{RawDump: true},
				configure.GetDefaultCliOpts(),
			},
			wantWriter: `./testdata/tt_cfg.yaml:
env:
  inc_dir: ./test_inc
  log_maxsize: 1024
app:
  wal_dir: ./wal
modules:
  directory: /root/modules
`,
			wantErr: false,
		},
		{
			name: "Default config dump",
			args: args{
				&cmdcontext.CmdCtx{
					Cli: cmdcontext.CliCtx{
						ConfigPath: "./testdata/tt_cfg.yaml",
					},
				},
				&DumpCtx{RawDump: false},
				getCliOpts(t, "testdata/tt_cfg.yaml"),
			},
			wantWriter: fmt.Sprintf(`./testdata/tt_cfg.yaml:
env:
  bin_dir: %[1]s/bin
  inc_dir: %[1]s/test_inc
  instances_enabled: .
  log_maxsize: 1024
  log_maxage: 8
  log_maxbackups: 10
  restart_on_failure: false
  tarantoolctl_layout: false
modules:
  directory: /root/modules
app:
  run_dir: %[1]s/var/run
  log_dir: %[1]s/var/log
  wal_dir: %[1]s/wal
  memtx_dir: %[1]s/var/lib
  vinyl_dir: %[1]s/var/lib
ee:
  credential_path: ""
templates: []
repo:
  rocks: ""
  distfiles: %[1]s/distfiles
`, configDir),
			wantErr: false,
		},
		{
			name: "Another config dump",
			args: args{
				&cmdcontext.CmdCtx{
					Cli: cmdcontext.CliCtx{
						ConfigPath: "./testdata/tt_cfg2.yaml",
					},
				},
				&DumpCtx{RawDump: false},
				getCliOpts(t, "testdata/tt_cfg2.yaml"),
			},
			wantWriter: fmt.Sprintf(`./testdata/tt_cfg2.yaml:
env:
  bin_dir: %[1]s/bin
  inc_dir: %[1]s/include
  instances_enabled: %[1]s/instances.enabled
  log_maxsize: 100
  log_maxage: 8
  log_maxbackups: 10
  restart_on_failure: false
  tarantoolctl_layout: false
modules:
  directory: %[1]s/my_modules
app:
  run_dir: %[1]s/var/run
  log_dir: %[1]s/var/log
  wal_dir: %[1]s/var/lib
  memtx_dir: %[1]s/var/lib
  vinyl_dir: %[1]s/var/lib
ee:
  credential_path: ""
templates:
- path: %[1]s/my_templates
- path: /tmp/templates
repo:
  rocks: ""
  distfiles: %[1]s/distfiles
`, configDir),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &bytes.Buffer{}
			err := RunDump(writer, tt.args.cmdCtx, tt.args.dumpCtx, tt.args.cliOpts)
			if tt.wantErr {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}
			gotWriter := writer.String()
			require.EqualValues(t, tt.wantWriter, gotWriter)
		})
	}
}
