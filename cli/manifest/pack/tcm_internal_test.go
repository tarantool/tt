package pack

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// tcmTemplate is a template that runs TCM, with fields, YAML of the fields
// of its record.
func tcmTemplate(fields string) map[string]string {
	return map[string]string{
		".templates/default/instances.yaml": "tcm:\n  kind: tcm\n" + fields,
	}
}

// enterprise returns man on Tarantool Enterprise.
func enterprise(man *manifest.Manifest) *manifest.Manifest {
	man.Platform.Tarantool.Flavor = "ee"

	return man
}

// TestStageNeverPacksProjectState pins that nothing of the scope root of the
// project, .tarantool/, reaches the archive: the data, the logs and the run
// files of its instances, and the password tt generates for TCM, whatever is
// next to them in the project.
func TestStageNeverPacksProjectState(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, withTemplates(tcmTemplate(""), map[string]string{
		".tarantool/var/lib/my-app/tcm/admin.secret":     "secret\n",
		".tarantool/var/lib/my-app/tcm/etcd/member/wal":  "data",
		".tarantool/var/run/my-app/tcm/tcm.yaml":         "http: {}\n",
		".tarantool/var/log/my-app/tcm/tt.log":           "log\n",
		".tarantool/versions/tcm/tcm_1.3.1/tcm":          "binary",
		".tarantool/var/lib/my-app/storage-1/00000.snap": "snap",
		".templates/default/README":                      "the template's own",
		"var/lib/my-app/storage-1/00000.xlog":            "the old layout",
	}))
	stageDir := t.TempDir()

	require.NoError(t, stage(t.Context(), stageDir,
		baseRequest(projectDir, enterprise(man), testTree(t, projectDir))))

	for _, name := range stagedNames(t, stageDir) {
		assert.False(t, strings.HasPrefix(name, ".tarantool"), name)
		assert.NotContains(t, name, "admin.secret")
		assert.False(t, strings.HasPrefix(name, "var/"), name)
	}

	// An include of the scope root, or of anything in it, fails the pack.
	man.Package.Include = append(man.Package.Include, ".tarantool/var")

	err := stage(t.Context(), t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir)))
	require.ErrorIs(t, err, errProjectState)
}

func TestStageTemplatesRefusesTCMOnCommunity(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, tcmTemplate(""))

	err := stage(t.Context(), t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir)))
	require.ErrorIs(t, err, errBadTemplate)
	require.ErrorIs(t, err, manifest.ErrTCMCommunity)
	require.ErrorContains(t, err, `.templates/default: invalid instance template: instance `+
		`"tcm" is TCM: TCM is Tarantool Enterprise only`)

	require.NoError(t, stage(t.Context(), t.TempDir(),
		baseRequest(projectDir, enterprise(man), testTree(t, projectDir))))
}

func TestStageTemplatesRefusesTCMSecretInTemplate(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, withTemplates(tcmTemplate("  password_file: admin.secret\n"),
		map[string]string{".templates/default/admin.secret": "secret\n"}))

	err := stage(t.Context(), t.TempDir(),
		baseRequest(projectDir, enterprise(man), testTree(t, projectDir)))
	require.ErrorIs(t, err, errBadTemplate)
	require.ErrorContains(t, err, "password_file admin.secret is in the template")

	// A password file outside of the template is the host's, and is fine.
	projectDir, man = testProject(t, tcmTemplate("  password_file: /run/secrets/tcm\n"))

	require.NoError(t, stage(t.Context(), t.TempDir(),
		baseRequest(projectDir, enterprise(man), testTree(t, projectDir))))
}
