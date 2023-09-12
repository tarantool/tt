package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/pack/test_helpers"
)

func Test_initSystemdDir(t *testing.T) {
	baseTestDir := t.TempDir()

	prefixToUnit := filepath.Join("usr", "lib", "systemd", "system")
	fakeCfgPath := "/path/to/cfg"

	var (
		test1Dir = "test_default_template_values"
		test2Dir = "test_default_template_partly_defined_values"
		test3Dir = "test_default_template_fully_defined_values"
		appDir   = "app"
	)
	testDirs := []string{
		filepath.Join(test1Dir, appDir),
		filepath.Join(test2Dir, appDir),
		filepath.Join(test3Dir, appDir),
	}

	err := test_helpers.CreateDirs(baseTestDir, testDirs)
	require.NoError(t, err)

	require.NoError(t, copy.Copy(filepath.Join("testdata", "partly-defined-params.yaml"),
		filepath.Join(baseTestDir, test2Dir, "partly-defined-params.yaml")))
	require.NoError(t, copy.Copy(filepath.Join("testdata", "fully-defined-params.yaml"),
		filepath.Join(baseTestDir, test3Dir, "fully-defined-params.yaml")))

	require.NoError(t, copy.Copy(filepath.Join("testdata", "expected-unit-content-1.txt"),
		filepath.Join(baseTestDir, test1Dir, prefixToUnit, "expected-unit-content-1.txt")))
	require.NoError(t, copy.Copy(filepath.Join("testdata", "expected-unit-content-2.txt"),
		filepath.Join(baseTestDir, test2Dir, prefixToUnit, "expected-unit-content-2.txt")))
	require.NoError(t, copy.Copy(filepath.Join("testdata", "expected-unit-content-3.txt"),
		filepath.Join(baseTestDir, test3Dir, prefixToUnit, "expected-unit-content-3.txt")))

	type args struct {
		baseDirPath string
		pathToEnv   string
		opts        *config.CliOpts
		packCtx     *PackCtx
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
		check   func() error
	}{
		{
			name: "Default template and values test 1",
			args: args{
				baseDirPath: filepath.Join(baseTestDir, test1Dir),
				pathToEnv:   fakeCfgPath,
				opts: &config.CliOpts{
					App: &config.AppOpts{
						InstancesEnabled: filepath.Join(baseTestDir, test1Dir),
					},
				},
				packCtx: &PackCtx{
					Name:    "pack",
					AppList: []string{appDir},
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			check: func() error {
				content, err := os.ReadFile(filepath.Join(baseTestDir,
					test1Dir, prefixToUnit, "pack.service"))
				if err != nil {
					return err
				}
				contentStr := string(content)

				expectedContent, err := os.ReadFile(filepath.Join(baseTestDir,
					test1Dir, prefixToUnit, "expected-unit-content-1.txt"))
				if err != nil {
					return err
				}
				expectedContentStr := string(expectedContent)

				if contentStr != expectedContentStr {
					return fmt.Errorf("the unit file doesn't contain the passed value")
				}
				return nil
			},
		},
		{
			name: "Default template and partly defined values test 2",
			args: args{
				baseDirPath: filepath.Join(baseTestDir, test2Dir),
				pathToEnv:   fakeCfgPath,
				opts: &config.CliOpts{
					App: &config.AppOpts{
						InstancesEnabled: filepath.Join(baseTestDir, test2Dir),
					},
				},
				packCtx: &PackCtx{
					Name:    "pack",
					AppList: []string{appDir},
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: filepath.Join(baseTestDir,
							test2Dir, "partly-defined-params.yaml"),
					},
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			check: func() error {
				content, err := os.ReadFile(filepath.Join(baseTestDir,
					test2Dir, prefixToUnit, "pack.service"))
				if err != nil {
					return err
				}
				contentStr := string(content)

				expectedContent, err := os.ReadFile(filepath.Join(baseTestDir,
					test2Dir, prefixToUnit, "expected-unit-content-2.txt"))
				if err != nil {
					return err
				}
				expectedContentStr := string(expectedContent)

				if contentStr != expectedContentStr {
					return fmt.Errorf("the unit file doesn't contain the passed value")
				}
				return nil
			},
		},
		{
			name: "Default template and fully defined values test 3",
			args: args{
				baseDirPath: filepath.Join(baseTestDir, test3Dir),
				pathToEnv:   fakeCfgPath,
				opts: &config.CliOpts{
					App: &config.AppOpts{
						InstancesEnabled: filepath.Join(baseTestDir, test3Dir),
					},
				},
				packCtx: &PackCtx{
					Name:    "pack",
					AppList: []string{appDir},
					RpmDeb: RpmDebCtx{
						SystemdUnitParamsFile: filepath.Join(baseTestDir,
							test3Dir, "fully-defined-params.yaml"),
					},
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			check: func() error {
				content, err := os.ReadFile(filepath.Join(baseTestDir,
					test3Dir, prefixToUnit, "pack.service"))
				if err != nil {
					return err
				}
				contentStr := string(content)

				expectedContent, err := os.ReadFile(filepath.Join(baseTestDir,
					test3Dir, prefixToUnit, "expected-unit-content-3.txt"))
				if err != nil {
					return err
				}
				expectedContentStr := string(expectedContent)

				if contentStr != expectedContentStr {
					return fmt.Errorf("the unit file doesn't contain the passed value")
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, initSystemdDir(tt.args.packCtx, tt.args.opts,
				tt.args.baseDirPath, tt.args.pathToEnv),
				fmt.Sprintf("initSystemdDir(%v, %v, %v, %v)",
					tt.args.baseDirPath, tt.args.pathToEnv, tt.args.opts, tt.args.packCtx))

			assert.NoError(t, tt.check())
		})
	}
}

func Test_getUnitParams(t *testing.T) {
	testDir := t.TempDir()

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
				},
			},
			want: map[string]interface{}{
				"TT":         "tt",
				"ConfigPath": "/path/to/env",
				"FdLimit":    defaultInstanceFdLimit,
				"EnvName":    "envName",
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
				},
			},
			want: map[string]interface{}{
				"TT":         "tt",
				"ConfigPath": "/path/to/env",
				"FdLimit":    1024,
				"EnvName":    "envName",
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
				},
			},
			want: map[string]interface{}{
				"TT":         "/usr/bin/tt",
				"ConfigPath": "/test/path",
				"FdLimit":    1024,
				"EnvName":    "testEnv",
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err != nil
			},
			prepare: func() error {
				err := os.WriteFile(filepath.Join(testDir, "fully-params.yaml"),
					[]byte("FdLimit: 1024\n"+
						"TT: /usr/bin/tt\n"+
						"ConfigPath: /test/path\n"+
						"EnvName: testEnv\n"), 0666)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.prepare())
			got, err := getUnitParams(tt.args.packCtx, tt.args.pathToEnv, tt.args.envName)
			if !tt.wantErr(t, err, fmt.Sprintf("getUnitParams(%v, %v, %v)",
				tt.args.packCtx, tt.args.pathToEnv, tt.args.envName)) {
				return
			}
			assert.Equalf(t, tt.want, got, "getUnitParams(%v, %v, %v)",
				tt.args.packCtx, tt.args.pathToEnv, tt.args.envName)
		})
	}
}
