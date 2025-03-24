// FIXME: Create new tests https://github.com/tarantool/tt/issues/1039

package modules_test

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/modules"
)

func TestGetModulesInfo(t *testing.T) {
	tests := map[string]struct {
		config      string
		modules     []string
		env_modules string
		want        modules.ModulesInfo
		err         string
		log         []string
	}{
		"no config": {
			config: "",
			want:   modules.ModulesInfo{},
		},

		"no external modules": {
			config:  "some/config/tt.yaml",
			modules: []string{},
			want:    modules.ModulesInfo{},
		},

		"nil modules": {
			config:  "some/config/tt.yaml",
			modules: nil,
			want:    modules.ModulesInfo{},
		},

		"config modules": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/modules1"},
			want: modules.ModulesInfo{
				"root ext_mod": modules.Manifest{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": modules.Manifest{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
		},

		"env modules": {
			config:      "some/config/tt.yaml",
			env_modules: "testdata/modules1:testdata/modules2",
			want: modules.ModulesInfo{
				"root ext_mod": modules.Manifest{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root ext_mod2": modules.Manifest{
					Name:    "ext_mod2",
					Main:    "testdata/modules2/ext_mod2/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": modules.Manifest{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
		},

		"no config but env modules": {
			config:      "",
			env_modules: "testdata/modules1",
			want: modules.ModulesInfo{
				"root ext_mod": modules.Manifest{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": modules.Manifest{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
		},

		"config and env modules": {
			config:      "some/config/tt.yaml",
			modules:     []string{"testdata/modules1"},
			env_modules: "testdata/modules2",
			want: modules.ModulesInfo{
				"root ext_mod": modules.Manifest{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root ext_mod2": modules.Manifest{
					Name:    "ext_mod2",
					Main:    "testdata/modules2/ext_mod2/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": modules.Manifest{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
		},

		"config duplicate env modules": {
			config:      "some/config/tt.yaml",
			modules:     []string{"testdata/modules1"},
			env_modules: "testdata/modules1",
			want: modules.ModulesInfo{
				"root ext_mod": modules.Manifest{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": modules.Manifest{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
			log: []string{"Ignore duplicate module"},
		},

		"wrong modules manifest": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/bad_manifest"},
			want:    modules.ModulesInfo{},
			log: []string{
				`Failed to get information about module "empty": failed to find module executable`,
				`Failed to get information about module "not-exists":` +
					` failed to find module executable`,
				`Failed to get information about module "no-ver": version field is mandatory`,
				`Failed to get information about module "no-help": help field is mandatory`,
				`Failed to get information about module "not-mf": failed to read manifest`,
				`Failed to get information about module "broken": failed to parse manifest`,
				`Failed to get information about module "no_version":` +
					` reply for --version is mandatory for module`,
				`Failed to get information about module "simple": can't parse module info`,
			},
		},

		"not a directory in config ": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/modules1/simple/main"},
			want:    modules.ModulesInfo{},
			err:     "specified path in configuration file is not a directory",
		},

		"not a directory in env ": {
			config:      "some/config/tt.yaml",
			env_modules: "testdata/modules1/simple/main",
			want:        modules.ModulesInfo{},
			err:         "specified path in configuration file is not a directory",
		},

		"override internal": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/mod_override"},
			want: modules.ModulesInfo{
				"root testCmd": modules.Manifest{
					Name:    "testCmd",
					Main:    "testdata/mod_override/testCmd/main",
					Help:    "Description for testCmd module",
					Version: "v1.2.3",
				},
			},
		},
		"disabled override ": {
			config:      "some/config/tt.yaml",
			env_modules: "testdata/disabled_override",
			want:        modules.ModulesInfo{},
			err:         `module "modules" is disabled to override`,
		},
	}

	os.Unsetenv("TT_CLI_MODULES_PATH")
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cmdCtx := cmdcontext.CmdCtx{
				Cli: cmdcontext.CliCtx{
					ConfigPath: tt.config,
				},
			}
			cliOpts := config.CliOpts{
				Modules: &config.ModulesOpts{
					Directories: tt.modules,
				},
			}
			if tt.env_modules != "" {
				os.Setenv("TT_CLI_MODULES_PATH", tt.env_modules)
			}
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer func() {
				log.SetOutput(os.Stderr)
			}()

			got, err := modules.GetModulesInfo(&cmdCtx, "root", &cliOpts)
			os.Unsetenv("TT_CLI_MODULES_PATH")
			t.Log(buf.String())

			if err != nil || tt.err != "" {
				assert.NotNil(t, err, "Expecting msg: %q", tt.err)
				assert.ErrorContains(t, err, tt.err)
				return
			}
			assert.EqualValues(t, tt.want, got)
			if len(tt.log) > 0 {
				for _, log := range tt.log {
					assert.Contains(t, buf.String(), log)
				}
			}
		})
	}
}
