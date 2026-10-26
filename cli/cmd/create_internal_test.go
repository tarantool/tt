package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestCreateValidArgsFunction(t *testing.T) {
	templates := []string{
		"vshard_cluster",
		"single_instance",
		"config_storage",
		"cluster",
	}

	t.Run("empty args", func(t *testing.T) {
		actualTemplates, dir := createValidArgsFunction(&cobra.Command{},
			[]string{}, "")
		assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
		assert.ElementsMatch(t, templates, actualTemplates)
	})

	// A template of the user's own is a path, which the shell completes.
	t.Run("path", func(t *testing.T) {
		actualTemplates, dir := createValidArgsFunction(&cobra.Command{},
			[]string{}, "./tem")
		assert.Equal(t, cobra.ShellCompDirectiveDefault, dir)
		assert.Empty(t, actualTemplates)
	})

	t.Run("non empty args", func(t *testing.T) {
		actualTemplates, dir := createValidArgsFunction(&cobra.Command{},
			[]string{"template"}, "")
		assert.Equal(t, cobra.ShellCompDirectiveDefault, dir)
		assert.Equal(t, []string(nil), actualTemplates)
	})
}
