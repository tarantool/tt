package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/running"
)

func compareFiles(t *testing.T, resultFile string, expectedFile string) {
	actualContent, err := os.ReadFile(resultFile)
	require.NoError(t, err)
	actualContentStr := string(actualContent)

	expectedContent, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	expectedContentStr := string(expectedContent)

	assert.Equal(t, expectedContentStr, actualContentStr)
}

func Test_initSystemdDir(t *testing.T) {
	prefixToUnit := filepath.Join("usr", "lib", "systemd", "system")
	fakeCfgPath := "/path/to/cfg"

	var (
		appDir   = "app"
		appsInfo = map[string][]running.InstanceCtx{
			"app": {
				running.InstanceCtx{
					AppName:   "app",
					SingleApp: false,
				},
			},
		}
	)

	type args struct {
		pathToEnv string
		opts      *config.CliOpts
		packCtx   *PackCtx
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
		check   func(baseTestDir string) error
	}{
		{
			name: "Default template and values test 1",
			args: args{
				pathToEnv: fakeCfgPath,
				opts: &config.CliOpts{
					Env: &config.TtEnvOpts{},
				},
				packCtx: &PackCtx{
					Name:     "pack",
					AppList:  []string{appDir},
					AppsInfo: appsInfo,
				},
			},
			wantErr: assert.NoError,
			check: func(baseTestDir string) error {
				compareFiles(t, filepath.Join(baseTestDir, prefixToUnit, "app@.service"),
					filepath.Join("testdata", "expected-unit-content-1.txt"))
				return nil
			},
		},
		{
			name: "Default template and partly defined values test 2",
			args: args{
				pathToEnv: fakeCfgPath,
				opts: &config.CliOpts{
					Env: &config.TtEnvOpts{},
				},
				packCtx: &PackCtx{
					Name:    "pack",
					AppList: []string{appDir},
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: filepath.Join("testdata",
							"partly-defined-params.yaml"),
					},
					AppsInfo: appsInfo,
				},
			},
			wantErr: assert.NoError,
			check: func(baseTestDir string) error {
				compareFiles(t, filepath.Join(baseTestDir, prefixToUnit, "app@.service"),
					filepath.Join("testdata", "expected-unit-content-2.txt"))
				return nil
			},
		},
		{
			name: "Default template and fully defined values test 3",
			args: args{
				pathToEnv: fakeCfgPath,
				opts: &config.CliOpts{
					Env: &config.TtEnvOpts{},
				},
				packCtx: &PackCtx{
					Name:    "pack",
					AppList: []string{appDir},
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: filepath.Join("testdata",
							"fully-defined-params.yaml"),
					},
					AppsInfo: appsInfo,
				},
			},
			wantErr: assert.NoError,
			check: func(baseTestDir string) error {
				compareFiles(t, filepath.Join(baseTestDir, prefixToUnit, "app@.service"),
					filepath.Join("testdata", "expected-unit-content-3.txt"))
				return nil
			},
		},
		{
			name: "Systemd units generation for multiple applications env",
			args: args{
				pathToEnv: fakeCfgPath,
				opts: &config.CliOpts{
					Env: &config.TtEnvOpts{},
				},
				packCtx: &PackCtx{
					Name:    "pack",
					AppList: []string{"app1", "app2"},
					AppsInfo: map[string][]running.InstanceCtx{
						"app1": {
							running.InstanceCtx{
								AppName:   "app1",
								SingleApp: false,
							},
						},
						"app2": {
							running.InstanceCtx{
								AppName:   "app2",
								SingleApp: true,
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			check: func(baseTestDir string) error {
				unitTemplate, err := template.ParseFiles("templates/app-inst-unit-template.txt")
				require.NoError(t, err)
				// app1 systemd unit check.
				appInstData := map[string]any{
					"TT":         filepath.Join(fakeCfgPath, configure.BinPath, "tt"),
					"ConfigPath": fakeCfgPath,
					"FdLimit":    defaultInstanceFdLimit,
					"AppName":    "app1@%i",
					"ExecArgs":   "app1:%i",
				}
				strBuilder := strings.Builder{}
				unitTemplate.Execute(&strBuilder, appInstData)

				buf, err := os.ReadFile(filepath.Join(baseTestDir, prefixToUnit, "app1@.service"))
				require.NoError(t, err)
				actualContent := string(buf)
				assert.Equal(t, strBuilder.String(), actualContent)

				// app2 systems unit check.
				appInstData["AppName"] = "app2"
				appInstData["ExecArgs"] = "app2"
				strBuilder.Reset()
				unitTemplate.Execute(&strBuilder, appInstData)
				// app2 is single instance app, so unit file is not template unit file.
				buf, err = os.ReadFile(filepath.Join(baseTestDir, prefixToUnit, "app2.service"))
				require.NoError(t, err)
				actualContent = string(buf)
				assert.Equal(t, strBuilder.String(), actualContent)

				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseTestDir := t.TempDir()
			tt.args.opts.Env.InstancesEnabled = baseTestDir
			tt.wantErr(t, initSystemdDir(tt.args.packCtx, tt.args.opts, baseTestDir,
				tt.args.pathToEnv),
				fmt.Sprintf("initSystemdDir(%v, %v, %v, %v)", baseTestDir, tt.args.pathToEnv,
					tt.args.opts, tt.args.packCtx))

			assert.NoError(t, tt.check(baseTestDir))
		})
	}
}

func Test_getUnitParams(t *testing.T) {
	testDir := t.TempDir()

	appsInfo := map[string][]running.InstanceCtx{
		"envName": {
			running.InstanceCtx{
				AppName:   "envName",
				SingleApp: false,
			},
		},
	}

	type args struct {
		packCtx   *PackCtx
		pathToEnv string
		envName   string
	}
	tests := []struct {
		name    string
		args    args
		prepare func() error
		want    map[string]interface{}
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "default parameters",
			args: args{
				envName:   "envName",
				pathToEnv: "/path/to/env",
				packCtx: &PackCtx{
					WithoutBinaries: true,
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: "",
					},
					AppsInfo: appsInfo,
				},
			},
			want: map[string]interface{}{
				"TT":         "tt",
				"ConfigPath": "/path/to/env",
				"FdLimit":    defaultInstanceFdLimit,
				"AppName":    "envName",
				"ExecArgs":   "envName:%i",
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err != nil
			},
			prepare: func() error {
				return nil
			},
		},
		{
			name: "partly defined parameters",
			args: args{
				envName:   "envName",
				pathToEnv: "/path/to/env",
				packCtx: &PackCtx{
					WithoutBinaries: true,
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: filepath.Join(testDir, "partly-params.yaml"),
					},
					AppsInfo: appsInfo,
				},
			},
			want: map[string]interface{}{
				"TT":         "tt",
				"ConfigPath": "/path/to/env",
				"FdLimit":    1024,
				"AppName":    "envName",
				"ExecArgs":   "envName:%i",
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err != nil
			},
			prepare: func() error {
				err := os.WriteFile(filepath.Join(testDir, "partly-params.yaml"),
					[]byte("FdLimit: 1024\n"), 0666)
				return err
			},
		},
		{
			name: "fully defined parameters",
			args: args{
				envName:   "envName",
				pathToEnv: "/path/to/env",
				packCtx: &PackCtx{
					WithoutBinaries: true,
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: filepath.Join(testDir, "fully-params.yaml"),
					},
					AppsInfo: map[string][]running.InstanceCtx{
						"envName": {
							running.InstanceCtx{
								AppName:   "envName",
								SingleApp: false,
							},
						},
					},
				},
			},
			want: map[string]interface{}{
				"TT":         "/usr/bin/tt",
				"ConfigPath": "/test/path",
				"FdLimit":    1024,
				"AppName":    "envName",
				"ExecArgs":   "envName",
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err != nil
			},
			prepare: func() error {
				err := os.WriteFile(filepath.Join(testDir, "fully-params.yaml"),
					[]byte("FdLimit: 1024\n"+
						"TT: /usr/bin/tt\n"+
						"ConfigPath: /test/path\n"+
						"AppName: testEnv\n"), 0666)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.prepare())
			got, err := getUnitParams(tt.args.packCtx, tt.args.pathToEnv, running.InstanceCtx{
				AppName: tt.args.envName,
			})
			if !tt.wantErr(t, err, fmt.Sprintf("getUnitParams(%v, %v, %v)",
				tt.args.packCtx, tt.args.pathToEnv, tt.args.envName)) {
				return
			}
			assert.Equalf(t, tt.want, got, "getUnitParams(%v, %v, %v)",
				tt.args.packCtx, tt.args.pathToEnv, tt.args.envName)
		})
	}
}
