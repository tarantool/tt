package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/config"
)

func TestCreateValidArgsFunction(t *testing.T) {
	tempsDir1 := t.TempDir()
	tempsDir2 := t.TempDir()
	oldOpts := cliOpts
	cliOpts = &config.CliOpts{
		Templates: []config.TemplateOpts{
			{Path: tempsDir1},
			{Path: tempsDir2},
		},
	}
	defer func() {
		cliOpts = oldOpts
	}()
	os.Create(tempsDir1 + "/" + "excess.A")
	os.Create(tempsDir1 + "/" + "archive.tgz")
	tDir1 := filepath.Join(tempsDir1, "template1")
	assert.NoError(t, os.Mkdir(tDir1, 0o755))

	os.Create(tempsDir2 + "/" + "excess.B")
	os.Create(tempsDir2 + "/" + "template2.tar.gz")
	tDir2 := filepath.Join(tempsDir2, "template2")
	assert.NoError(t, os.Mkdir(tDir2, 0o755))

	_, tDir1Name := filepath.Split(tDir1)
	_, tDir2Name := filepath.Split(tDir2)

	templates := []string{
		"vshard_cluster",
		"single_instance",
		"config_storage",
		"cluster",
		"archive",
		"template2",
		tDir1Name,
		tDir2Name,
	}

	t.Run("empty args", func(t *testing.T) {
		actualTemplates, dir := createValidArgsFunction(&cobra.Command{},
			[]string{}, "")
		assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
		assert.ElementsMatch(t, templates, actualTemplates)
	})

	t.Run("non empty args", func(t *testing.T) {
		actualTemplates, dir := createValidArgsFunction(&cobra.Command{},
			[]string{"template"}, "")
		assert.Equal(t, cobra.ShellCompDirectiveDefault, dir)
		assert.Equal(t, []string(nil), actualTemplates)
	})
}
