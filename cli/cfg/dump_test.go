package cfg

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
)

type mockRepository struct{}

func (mock *mockRepository) Read(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (mock *mockRepository) ValidateAll() error {
	return nil
}

func getCliOpts(t *testing.T, configFile string) *config.CliOpts {
	cliOpts, configPath, err := configure.GetCliOpts(configFile,
		&mockRepository{})
	require.NoError(t, err)
	srcStat, err := os.Stat(configFile)
	require.NoError(t, err)
	loadedStat, err := os.Stat(configPath)
	require.NoError(t, err)
	require.True(t, os.SameFile(srcStat, loadedStat))
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
  instances_enabled: %[1]s
  restart_on_failure: false
  tarantoolctl_layout: false
modules:
  directory: /root/modules
app:
  run_dir: var/run
  log_dir: var/log
  wal_dir: ./wal
  memtx_dir: var/lib
  vinyl_dir: var/lib
ee:
  credential_path: ""
templates:
- path: %[1]s/templates
repo:
  rocks: ""
  distfiles: %[1]s/distfiles
`, configDir),
			wantErr: false,
		},
		{
			name: "Config dump with list modules",
			args: args{
				&cmdcontext.CmdCtx{
					Cli: cmdcontext.CliCtx{
						ConfigPath: "./testdata/tt_cfg3.yaml",
					},
				},
				&DumpCtx{RawDump: false},
				getCliOpts(t, "testdata/tt_cfg3.yaml"),
			},
			wantWriter: fmt.Sprintf(`./testdata/tt_cfg3.yaml:
env:
  bin_dir: %[1]s/bin
  inc_dir: %[1]s/include
  instances_enabled: %[1]s
  restart_on_failure: false
  tarantoolctl_layout: false
modules:
  directory:
  - /root/modules
  - /some/other/modules
app:
  run_dir: var/run
  log_dir: var/log
  wal_dir: var/lib
  memtx_dir: var/lib
  vinyl_dir: var/lib
ee:
  credential_path: ""
templates:
- path: %[1]s/templates
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
  restart_on_failure: false
  tarantoolctl_layout: false
modules:
  directory: %[1]s/my_modules
app:
  run_dir: var/run
  log_dir: var/log
  wal_dir: var/lib
  memtx_dir: var/lib
  vinyl_dir: var/lib
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
		{
			name: "Config dump, config dir is app dir",
			args: args{
				&cmdcontext.CmdCtx{
					Cli: cmdcontext.CliCtx{
						ConfigPath: "./testdata/app_dir/tt_cfg.yaml",
					},
				},
				&DumpCtx{RawDump: false},
				getCliOpts(t, "testdata/app_dir/tt_cfg.yaml"),
			},
			wantWriter: fmt.Sprintf(`./testdata/app_dir/tt_cfg.yaml:
env:
  bin_dir: %[1]s/bin
  inc_dir: %[1]s/test_inc
  instances_enabled: .
  restart_on_failure: false
  tarantoolctl_layout: false
modules:
  directory: /root/modules
app:
  run_dir: var/run
  log_dir: var/log
  wal_dir: ./wal
  memtx_dir: var/lib
  vinyl_dir: var/lib
ee:
  credential_path: ""
templates:
- path: %[1]s/templates
repo:
  rocks: ""
  distfiles: %[1]s/distfiles
`, filepath.Join(cwd, "testdata", "app_dir")),
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
