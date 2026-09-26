package modules_test

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/modules"
)

func TestGetModulesInfo(t *testing.T) {
	tests := map[string]struct {
		envModules string
		want       modules.ModulesInfo
		err        string
		log        []string
	}{
		"no modules path": {
			envModules: "",
			want:       modules.ModulesInfo{},
		},

		"single directory": {
			envModules: "testdata/modules1",
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

		"several directories": {
			envModules: "testdata/modules1:testdata/modules2",
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

		"duplicate modules": {
			envModules: "testdata/modules1:testdata/modules1",
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
			envModules: "testdata/bad_manifest",
			want:       modules.ModulesInfo{},
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

		"not a directory": {
			envModules: "testdata/modules1:testdata/modules1/simple/main",
			err: "TT_CLI_MODULES_PATH names a path that is not a directory:" +
				" testdata/modules1/simple/main",
		},

		"override internal": {
			envModules: "testdata/mod_override",
			want: modules.ModulesInfo{
				"root testCmd": modules.Manifest{
					Name:    "testCmd",
					Main:    "testdata/mod_override/testCmd/main",
					Help:    "Description for testCmd module",
					Version: "v1.2.3",
				},
			},
		},

		"disabled override": {
			envModules: "testdata/disabled_override",
			err:        `module "modules" is disabled to override`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TT_CLI_MODULES_PATH", tt.envModules)

			var buf bytes.Buffer

			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			got, err := modules.GetModulesInfo("root")

			t.Log(buf.String())

			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			for _, message := range tt.log {
				assert.Contains(t, buf.String(), message)
			}
		})
	}
}
