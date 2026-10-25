package pack

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/internal/instances"
)

// validTemplate is a complete template: a description that parses and the
// config that goes with it.
func validTemplate(name string) map[string]string {
	return map[string]string{
		".templates/" + name + "/instances.yaml": "router-1:\nstorage-1: {}\n",
		".templates/" + name + "/config.yaml":    "groups: {}\n",
	}
}

// withTemplates merges template files into one map for testProject.
func withTemplates(sets ...map[string]string) map[string]string {
	out := map[string]string{}

	for _, set := range sets {
		maps.Copy(out, set)
	}

	return out
}

// templateNames filters the archive or staging names down to .templates/.
func templateNames(names []string) []string {
	var out []string

	for _, name := range names {
		if strings.HasPrefix(name, templatesDirName+"/") {
			out = append(out, name)
		}
	}

	return out
}

// TestReservedArchiveNamesMatchRFC pins the reserved set to the manifest RFC's
// definition of the reserved names of the archive. The two lists are one list:
// a name changed here without the RFC, or the other way round, fails this.
func TestReservedArchiveNamesMatchRFC(t *testing.T) {
	t.Parallel()

	// RFC-0010, glossary entry "зарезервированное имя в архиве".
	rfc := []string{
		"app.manifest.toml",
		"app.manifest.lock",
		"VERSION",
		".rocks",
		"_runtime",
		".templates",
	}

	assert.Equal(t, rfc, reservedArchiveNames)

	for _, name := range rfc {
		assert.True(t, isReservedName(name), name)
		assert.True(t, isReservedName(name+"/nested/file"), name)
	}
}

// TestStageTemplatesWhateverTheInclude is the core of the change: .templates/
// reaches the archive root whole, whatever [package].include says and in both
// packing modes. Before, the component filter dropped every hidden name and
// nothing in the manifest could bring it back.
func TestStageTemplatesWhateverTheInclude(t *testing.T) {
	t.Parallel()

	extra := map[string]string{
		// A template may carry more than the pair; it travels along.
		".templates/default/README.md": "how to run it",
	}

	tests := []struct {
		name     string
		include  []string
		withDeps bool
	}{
		{"default include", []string{"README.md"}, true},
		{"no include", nil, true},
		{"without deps", []string{"README.md"}, false},
		{"without deps and no include", nil, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			projectDir, man := testProject(t,
				withTemplates(validTemplate("default"), validTemplate("prod"), extra))
			tree := testTree(t, projectDir)

			man.Package.Include = test.include

			req := baseRequest(projectDir, man, tree)

			req.WithDeps = test.withDeps

			assert.Equal(t, []string{
				".templates/",
				".templates/default/",
				".templates/default/README.md",
				".templates/default/config.yaml",
				".templates/default/instances.yaml",
				".templates/prod/",
				".templates/prod/config.yaml",
				".templates/prod/instances.yaml",
			}, templateNames(packArchive(t, req)))
		})
	}
}

// TestStageTemplatesContentIsVerbatim checks the files are copied, not
// re-rendered: the archive carries what the author wrote.
func TestStageTemplatesContentIsVerbatim(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, validTemplate("default"))
	stageDir := t.TempDir()

	require.NoError(t, stage(stageDir, baseRequest(projectDir, man, testTree(t, projectDir))))

	staged, err := os.ReadFile(filepath.Join(stageDir, ".templates", "default", "instances.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "router-1:\nstorage-1: {}\n", string(staged))
}

// TestStageTemplatesSkipsHiddenEntries: a hidden entry directly under
// .templates/, the .gitkeep that keeps the directory in git, is neither a
// stray file that fails the pack nor packed. A hidden file inside a template
// is the template's own and travels with it.
func TestStageTemplatesSkipsHiddenEntries(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, withTemplates(validTemplate("default"), map[string]string{
		".templates/.gitkeep":         "",
		".templates/.hidden/x":        "not a template",
		".templates/default/.gitkeep": "",
	}))

	assert.Equal(t, []string{
		".templates/",
		".templates/default/",
		".templates/default/.gitkeep",
		".templates/default/config.yaml",
		".templates/default/instances.yaml",
	}, templateNames(packArchive(t, baseRequest(projectDir, man, testTree(t, projectDir)))))
}

// TestStageTemplatesOnlyGitkeep: a .templates/ holding nothing but a .gitkeep
// packs as an empty directory, as an empty .templates/ does.
func TestStageTemplatesOnlyGitkeep(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, map[string]string{".templates/.gitkeep": ""})

	assert.Equal(t, []string{".templates/"},
		templateNames(packArchive(t, baseRequest(projectDir, man, testTree(t, projectDir)))))
}

// TestStageWithoutTemplatesIsUnchanged: a project with no .templates/ packs
// exactly as it did before (TestStageWithDeps pins the full list).
func TestStageWithoutTemplatesIsUnchanged(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)
	stageDir := t.TempDir()

	require.NoError(t, stage(stageDir, baseRequest(projectDir, man, testTree(t, projectDir))))

	assert.NoDirExists(t, filepath.Join(stageDir, templatesDirName))
	assert.Empty(t, templateNames(stagedNames(t, stageDir)))
}

// TestStageTemplatesRelativePathsAreNotChecked: paths in a description are
// resolved, never looked up. A binary that exists only after the build, or
// only on the host, does not fail the pack.
func TestStageTemplatesRelativePathsAreNotChecked(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, map[string]string{
		".templates/default/instances.yaml": "worker-1:\n  binary: ./bin/worker\n" +
			"  workdir: ./var/worker\n",
		".templates/default/config.yaml": "groups: {}\n",
	})

	require.NoError(t, stage(t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir))))
}

// TestStageTemplatesRejectsBrokenTemplates covers every way a template is
// refused. Each is a pack error naming the template, exit 1, and nothing is
// archived.
func TestStageTemplatesRejectsBrokenTemplates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		// mention are substrings the error must carry.
		mention []string
		// invalid marks a description the instances parser refused.
		invalid bool
	}{
		{
			name: "missing config",
			files: map[string]string{
				".templates/default/instances.yaml": "router-1:\n",
			},
			mention: []string{".templates/default", "missing config.yaml"},
		},
		{
			name: "missing instances",
			files: map[string]string{
				".templates/default/config.yaml": "groups: {}\n",
			},
			mention: []string{".templates/default", "missing instances.yaml"},
		},
		{
			name: "empty template",
			files: map[string]string{
				".templates/default/notes.txt": "nothing else",
			},
			mention: []string{".templates/default", "missing instances.yaml and config.yaml"},
		},
		{
			name: "unknown field",
			files: map[string]string{
				".templates/default/instances.yaml": "router-1:\n  bniary: ./bin/x\n",
				".templates/default/config.yaml":    "groups: {}\n",
			},
			mention: []string{".templates/default/instances.yaml", "bniary"},
			invalid: true,
		},
		{
			name: "yaml syntax error",
			files: map[string]string{
				".templates/default/instances.yaml": "router-1: [\n",
				".templates/default/config.yaml":    "groups: {}\n",
			},
			mention: []string{".templates/default/instances.yaml"},
			invalid: true,
		},
		{
			name: "file directly under templates",
			files: withTemplates(validTemplate("default"), map[string]string{
				".templates/README.md": "stray",
			}),
			mention: []string{".templates/README.md", "not a directory"},
		},
		{
			name: "pair file is a directory",
			files: map[string]string{
				".templates/default/instances.yaml/x": "router-1:\n",
				".templates/default/config.yaml":      "groups: {}\n",
			},
			mention: []string{".templates/default", "instances.yaml", "not a regular file"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			projectDir, man := testProject(t, test.files)
			stageDir := t.TempDir()

			err := stage(stageDir, baseRequest(projectDir, man, testTree(t, projectDir)))

			require.Error(t, err)
			require.ErrorIs(t, err, errBadTemplate)
			assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))

			for _, want := range test.mention {
				assert.Contains(t, err.Error(), want)
			}

			if test.invalid {
				require.ErrorIs(t, err, instances.ErrInvalid)
			}

			assert.NoDirExists(t, filepath.Join(stageDir, templatesDirName))
		})
	}
}

// TestStageTemplatesNotADirectory: .templates as a file is refused, not
// mistaken for a project without templates.
func TestStageTemplatesNotADirectory(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, map[string]string{".templates": "not a dir"})

	err := stage(t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir)))

	require.Error(t, err)
	require.ErrorIs(t, err, errBadTemplate)
	assert.Contains(t, err.Error(), "not a directory")
}

// TestStageTemplatesReportsEveryBrokenTemplate: several broken templates are
// reported together, each line naming its template, so they are fixed in one
// pass.
func TestStageTemplatesReportsEveryBrokenTemplate(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, withTemplates(validTemplate("good"), map[string]string{
		".templates/half/instances.yaml": "router-1:\n",
		".templates/bad/instances.yaml":  "a:\n  bniary: x\nb:\n  restart: sometimes\n",
		".templates/bad/config.yaml":     "groups: {}\n",
	}))

	err := stage(t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir)))
	require.Error(t, err)

	lines := strings.Split(err.Error(), "\n")
	require.Len(t, lines, 3, err.Error())

	assert.Contains(t, lines[0], ".templates/bad/instances.yaml")
	assert.Contains(t, lines[0], "bniary")
	assert.Contains(t, lines[1], ".templates/bad/instances.yaml")
	assert.Contains(t, lines[1], "restart")
	assert.Contains(t, lines[2], ".templates/half")
	assert.NotContains(t, err.Error(), ".templates/good")
}

// TestStageIncludeCollidingWithTemplates: .templates/ is tt's at the archive
// root, so a payload entry cannot land on it, whichever case it is spelled in.
func TestStageIncludeCollidingWithTemplates(t *testing.T) {
	t.Parallel()

	for _, entry := range []string{
		".templates", ".templates/default", ".templates/default/config.yaml", ".TEMPLATES",
	} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()

			projectDir, man := testProject(t, validTemplate("default"))

			man.Package.Include = []string{entry}

			err := stage(t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir)))

			require.Error(t, err)
			require.ErrorIs(t, err, errReservedName)
			assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
		})
	}

	t.Run("license_files", func(t *testing.T) {
		t.Parallel()

		projectDir, man := testProject(t, validTemplate("default"))

		man.Package.LicenseFiles = []string{".templates/default/config.yaml"}

		err := stage(t.TempDir(), baseRequest(projectDir, man, testTree(t, projectDir)))

		require.ErrorIs(t, err, errReservedName)
	})
}

// TestStageTemplatesFollowsSymlinkedTemplate: a template linked in from
// elsewhere is checked and copied as its target, like every link pack meets.
func TestStageTemplatesFollowsSymlinkedTemplate(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)

	shared := filepath.Join(t.TempDir(), "shared")
	writeTree(t, shared, map[string]string{
		"instances.yaml": "router-1:\n",
		"config.yaml":    "groups: {}\n",
	})
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, templatesDirName), dirPerm))
	require.NoError(t, os.Symlink(shared, filepath.Join(projectDir, templatesDirName, "default")))

	stageDir := t.TempDir()
	require.NoError(t, stage(stageDir, baseRequest(projectDir, man, testTree(t, projectDir))))

	assert.Equal(t, []string{
		".templates/default/config.yaml",
		".templates/default/instances.yaml",
	}, templateNames(stagedNames(t, stageDir)))
}
