// FIXME: Create new tests https://github.com/tarantool/tt/issues/1039

package modules_test

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/modules"
)

func getTestRootCmd() *cobra.Command {
	testRootCmd := &cobra.Command{
		Use:   "root",
		Short: "root cmd",

		PersistentPreRun: func(cmd *cobra.Command, args []string) {},

		Run: func(cmd *cobra.Command, args []string) {},
	}

	var testCmd = &cobra.Command{
		Use:   "testCmd",
		Short: "test cmd",
	}

	var levelCmd1 = &cobra.Command{
		Use:   "levelCmd1",
		Short: "level 1",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	var levelCmd2 = &cobra.Command{
		Use:   "levelCmd2",
		Short: "level 2",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	testSubCommands := []*cobra.Command{
		levelCmd1,
		levelCmd2,
	}

	for _, cmd := range testSubCommands {
		testCmd.AddCommand(cmd)
	}

	testRootCmd.AddCommand(testCmd)

	return testRootCmd
}

func TestGetModulesInfo(t *testing.T) {
	rootCmd := getTestRootCmd()

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
			want: modules.ModulesInfo{
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"no external modules": {
			config:  "some/config/tt.yaml",
			modules: []string{},
			want: modules.ModulesInfo{
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"nil modules": {
			config:  "some/config/tt.yaml",
			modules: nil,
			want: modules.ModulesInfo{
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"config modules": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/modules1"},
			want: modules.ModulesInfo{
				"root ext_mod": &modules.Manifest{
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": &modules.Manifest{
					Main: "testdata/modules1/simple/main",
				},
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"env modules": {
			config:      "some/config/tt.yaml",
			env_modules: "testdata/modules1:testdata/modules2",
			want: modules.ModulesInfo{
				"root ext_mod": &modules.Manifest{
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root ext_mod2": &modules.Manifest{
					Main:    "testdata/modules2/ext_mod2/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": &modules.Manifest{
					Main: "testdata/modules1/simple/main",
				},
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"no config but env modules": {
			config:      "",
			env_modules: "testdata/modules1",
			want: modules.ModulesInfo{
				"root ext_mod": &modules.Manifest{
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": &modules.Manifest{
					Main: "testdata/modules1/simple/main",
				},
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"config and env modules": {
			config:      "some/config/tt.yaml",
			modules:     []string{"testdata/modules1"},
			env_modules: "testdata/modules2",
			want: modules.ModulesInfo{
				"root ext_mod": &modules.Manifest{
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root ext_mod2": &modules.Manifest{
					Main:    "testdata/modules2/ext_mod2/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": &modules.Manifest{
					Main: "testdata/modules1/simple/main",
				},
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
		},

		"config duplicate env modules": {
			config:      "some/config/tt.yaml",
			modules:     []string{"testdata/modules1"},
			env_modules: "testdata/modules1",
			want: modules.ModulesInfo{
				"root ext_mod": &modules.Manifest{
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				"root simple": &modules.Manifest{
					Main: "testdata/modules1/simple/main",
				},
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
			log: []string{"Ignore duplicate module"},
		},

		"wrong modules manifest": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/bad_manifest"},
			want: modules.ModulesInfo{
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
			log: []string{
				`Failed to get information about module "empty": failed to find module executable`,
				`Failed to get information about module "not-exists":` +
					`failed to find module executable`,
				`Failed to get information about module "no-ver": version field is mandatory`,
				`Failed to get information about module "no-help": help field is mandatory`,
				`Failed to get information about module "not-mf": failed to read manifest`,
				`Failed to get information about module "broken": failed to parse manifest`,
			},
		},

		"not a directory in config ": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/modules1/simple/main"},
			want: modules.ModulesInfo{
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
			err: "specified path in configuration file is not a directory",
		},

		"not a directory in env ": {
			config:      "some/config/tt.yaml",
			env_modules: "testdata/modules1/simple/main",
			want: modules.ModulesInfo{
				"root testCmd":           nil,
				"root testCmd levelCmd1": nil,
				"root testCmd levelCmd2": nil,
			},
			err: "specified path in configuration file is not a directory",
		},

		"override internal": {
			config:  "some/config/tt.yaml",
			modules: []string{"testdata/mod_override"},
			want: modules.ModulesInfo{
				"root testCmd": &modules.Manifest{
					Main: "testdata/mod_override/testCmd/main",
				},
			},
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

			got, err := modules.GetModulesInfo(&cmdCtx, rootCmd, &cliOpts)
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
