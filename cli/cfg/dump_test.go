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

func TestRunDump(t *testing.T) {
	type args struct {
		cmdCtx  *cmdcontext.CmdCtx
		dumpCtx *DumpCtx
		cliOpts *config.CliOpts
	}

	cliOpts, configPath, err := configure.GetCliOpts("./testdata/tt_cfg.yaml")
	require.NoError(t, err)
	require.Equal(t, "testdata/tt_cfg.yaml", configPath)

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
tt:
  app:
    inc_dir: ./test_inc
    log_maxsize: 1024
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
				cliOpts,
			},
			wantWriter: fmt.Sprintf(`./testdata/tt_cfg.yaml:
tt:
  modules:
    directory: /root/modules
  app:
    run_dir: %[1]s/var/run
    log_dir: %[1]s/var/log
    log_maxsize: 1024
    log_maxage: 8
    log_maxbackups: 10
    restart_on_failure: false
    data_dir: %[1]s/var/lib
    bin_dir: %[1]s/bin
    inc_dir: %[1]s/test_inc
    instances_enabled: .
  ee:
    credential_path: ""
  templates: []
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
