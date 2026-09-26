package extmod_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/core/internal/extmod"
)

// TestMountFindsModules checks which modules Mount finds in the directories
// the path lists, and what it warns about while it looks.
func TestMountFindsModules(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		envModules string
		want       []extmod.Manifest
		err        string
		log        []string
	}{
		"no modules path": {
			envModules: "",
			want:       []extmod.Manifest{},
		},

		"single directory": {
			envModules: "testdata/modules1",
			want: []extmod.Manifest{
				{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
		},

		"several directories": {
			envModules: "testdata/modules1:testdata/modules2",
			want: []extmod.Manifest{
				{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				{
					Name:    "ext_mod2",
					Main:    "testdata/modules2/ext_mod2/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
		},

		"a directory that does not exist": {
			envModules: "testdata/missing:testdata/modules2",
			want: []extmod.Manifest{
				{
					Name:    "ext_mod2",
					Main:    "testdata/modules2/ext_mod2/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
			},
		},

		"duplicate modules": {
			envModules: "testdata/modules1:testdata/modules1",
			want: []extmod.Manifest{
				{
					Name:    "ext_mod",
					Main:    "testdata/modules1/ext_mod/command.sh",
					Help:    "Help for the ext_mod module",
					Version: "1.2.3",
				},
				{
					Name:    "simple",
					Main:    "testdata/modules1/simple/main",
					Help:    "Description for simple module",
					Version: "v0.0.1",
				},
			},
			log: []string{
				`Ignore duplicate module "testdata/modules1/ext_mod" overlap with ` +
					`"testdata/modules1/ext_mod"`,
				`Ignore duplicate module "testdata/modules1/simple" overlap with ` +
					`"testdata/modules1/simple"`,
			},
		},

		"wrong modules manifest": {
			envModules: "testdata/bad_manifest",
			want:       []extmod.Manifest{},
			log: []string{
				`Failed to get information about module "broken": failed to parse manifest`,
				`Failed to get information about module "empty": failed to find module executable`,
				`Failed to get information about module "no-help": help field is mandatory`,
				`Failed to get information about module "no-ver": version field is mandatory`,
				`Failed to get information about module "no_version":` +
					` reply for --version is mandatory for module`,
				`Failed to get information about module "not-exists":` +
					` failed to find module executable`,
				`Failed to get information about module "not-mf": failed to read manifest`,
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
			want: []extmod.Manifest{
				{
					Name:    "testCmd",
					Main:    "testdata/mod_override/testCmd/main",
					Help:    "Description for testCmd module",
					Version: "v1.2.3",
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := newTree(t)

			got, err := tree.mount(tt.envModules)

			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			messages := tree.log.Messages()
			require.Len(t, messages, len(tt.log), strings.Join(messages, "\n"))

			for i, message := range tt.log {
				assert.Contains(t, messages[i], message)
			}
		})
	}
}
