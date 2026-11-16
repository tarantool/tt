package tcm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/tcm"
	"github.com/tarantool/tt/v3/internal/instances"
)

const projectDir = "/srv/shop"

// record is the record of TCM AddRecord writes, listening on http.
func record(http string) string {
	return "# TCM, the Tarantool Cluster Manager of the application, added by tt tcm init.\n" +
		"tcm:\n  kind: tcm\n  http: " + http + "\n" +
		"  # config: tcm.yaml   # settings of TCM of your own, relative to this file\n"
}

func TestAddRecord(t *testing.T) {
	cases := []struct {
		name       string
		src        []byte
		withConfig bool
		http       string
		want       string
	}{
		{
			name: "no description, no config",
			want: record(tcm.DefaultHTTP),
		},
		{
			name:       "no description next to a config",
			withConfig: true,
			http:       "0.0.0.0:8081",
			want: "# The instances of the topology of config.yaml, in the order of their " +
				"names.\n\"*\": {}\n\n" + record("0.0.0.0:8081"),
		},
		{
			name:       "a description, kept byte for byte",
			src:        []byte("# mine\n\"*\":\n  restart: always # keep\nweb: {binary: web}"),
			withConfig: true,
			want: "# mine\n\"*\":\n  restart: always # keep\nweb: {binary: web}\n\n" +
				record(tcm.DefaultHTTP),
		},
		{
			name: "an empty description",
			src:  []byte{},
			want: record(tcm.DefaultHTTP),
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, declared, err := tcm.AddRecord(test.src, projectDir, test.withConfig, test.http)
			require.NoError(t, err)
			assert.Empty(t, declared.Existing)
			assert.Equal(t, test.want, string(got))

			desc, err := instances.Parse(got, projectDir)
			require.NoError(t, err)

			last := desc.Instances[len(desc.Instances)-1]
			assert.Equal(t, instances.KindTCM, last.Kind())
			assert.Equal(t, test.withConfig, desc.Wildcard != nil)
		})
	}
}

func TestAddRecordExisting(t *testing.T) {
	src := []byte("manager:\n  kind: tcm\n  http: 127.0.0.1:9000\n")

	got, declared, err := tcm.AddRecord(src, projectDir, false, "127.0.0.1:8080")
	require.NoError(t, err)
	assert.Equal(t, src, got, "the description is left as it is")
	assert.Equal(t, tcm.Declared{Existing: "manager", HTTP: "127.0.0.1:9000"}, declared)
}

func TestAddRecordErrors(t *testing.T) {
	_, _, err := tcm.AddRecord([]byte("tcm:\n  binary: ./tcm\n"), projectDir, false, "")
	require.ErrorIs(t, err, tcm.ErrNameTaken)

	_, _, err = tcm.AddRecord([]byte("a: [\n"), projectDir, false, "")
	require.ErrorIs(t, err, instances.ErrInvalid)

	_, _, err = tcm.AddRecord(nil, projectDir, false, "localhost")
	require.ErrorIs(t, err, instances.ErrInvalid)
	assert.ErrorContains(t, err, `field "http": "localhost" is not an address`)
}

func TestConstraint(t *testing.T) {
	for given, want := range map[string]string{
		"1.3":      ">=1.3",
		">=1.3,<2": ">=1.3,<2",
		"~1.3":     "~1.3",
		"=1.3.1":   "=1.3.1",
		"":         "",
	} {
		assert.Equal(t, want, tcm.Constraint(given), given)
	}

	assert.Equal(t, ">=1.3.1", tcm.Constraint(" 1.3.1\n"), "the blanks around are dropped")
}

func TestNewestInstalled(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()

	for root, dirs := range map[string][]string{
		project: {"tcm_1.3.1-0-g074b5ffa", "tcm_sdk_3.9.0", "tcm_dev_abc"},
		user:    {"tcm_1.2.0", "tcm_1.4.0", "not-tcm"},
	} {
		for _, dir := range dirs {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "versions", "tcm", dir), 0o755))
		}
	}

	newest, found := tcm.NewestInstalled([]string{project, user, filepath.Join(user, "none")})
	require.True(t, found)
	assert.Equal(t, "1.4.0", newest)

	newest, found = tcm.NewestInstalled([]string{project})
	require.True(t, found)
	assert.Equal(t, "1.3.1", newest, "the release of a build, without its commit")

	_, found = tcm.NewestInstalled([]string{t.TempDir()})
	assert.False(t, found)
}
