package cmd

import (
	"flag"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmd/helpgolden"
	"github.com/tarantool/tt/v3/cli/modules"
	"github.com/tarantool/tt/v3/cli/version"
)

// update rewrites the golden files of this package instead of comparing
// against them: go test ./cli/cmd -run TestHelpFlavourGolden -update.
var update = flag.Bool("update", false, "rewrite golden files")

// helpFlavourGoldenPath is the golden file TestHelpFlavourGolden compares
// against.
const helpFlavourGoldenPath = "testdata/help_flavour.golden"

// eeFlavour is a distribution with a name, a version and an edition of its
// own.
var eeFlavour = version.Flavour{
	Title:   "Tarantool CLI EE",
	Version: &version.Info{Tag: "v2.15.0", Commit: "def5678", CommitsSinceTag: 0, Label: ""},
	Edition: "ee",
}

// TestCoreVersionIgnoresFlavour checks that the version [platform].tt is
// checked against and generated files are stamped with stays the core's
// when a distribution presents itself with a version of its own.
//
//nolint:paralleltest // Replaces the package root and the flavour.
func TestCoreVersionIgnoresFlavour(t *testing.T) {
	restoreProcessState(t)

	core := version.GetVersion(true, false)

	_, err := Boot(BootOptions{Args: nil, Flavour: eeFlavour})
	require.NoError(t, err)
	require.Equal(t, "2.15.0+ee", flavour.GetVersion(true, false),
		"the flavour must be in effect")

	assert.Equal(t, core, coreVersion())
	assert.NotContains(t, coreVersion(), "+ee")
}

// TestBootKeepsTtsOwnFlavour checks that booting without a flavour
// presents tt as itself, whatever flavour a previous boot set.
//
//nolint:paralleltest // Replaces the package root and the flavour.
func TestBootKeepsTtsOwnFlavour(t *testing.T) {
	restoreProcessState(t)

	flavour = eeFlavour

	root, err := Boot(BootOptions{Args: nil, Flavour: version.Flavour{}})
	require.NoError(t, err)

	assert.Equal(t, "Tarantool CLI", root.Short)
	assert.Equal(t, version.GetVersion(false, false), flavour.GetVersion(false, false))
}

// newFlavourRoot builds the command tree the way tt does before it runs a
// command, with the builtin commands and no external modules, for a
// distribution presenting itself as eeFlavour, and makes it the package's
// root.
func newFlavourRoot(t *testing.T) *cobra.Command {
	t.Helper()

	previousRoot, previousFlavour := rootCmd, flavour

	t.Cleanup(func() { rootCmd, flavour = previousRoot, previousFlavour })

	flavour = eeFlavour

	root := NewCmdRoot()
	configureHelpCommand(root, &modules.ModulesInfo{})

	rootCmd = root

	return root
}

// TestHelpFlavourGolden pins the help a distribution with a name of its own
// shows where tt names itself: the root and tt version.
//
//nolint:paralleltest // Replaces the package root, the flavour and os.Stdout.
func TestHelpFlavourGolden(t *testing.T) {
	got := helpgolden.RenderHelp(t, newFlavourRoot,
		"The help of a distribution named \"Tarantool CLI EE\" where tt names\n"+
			"itself. Regenerate with:\n"+
			"  go test ./cli/cmd -run TestHelpFlavourGolden -update",
		nil, []string{"version"})

	helpgolden.Compare(t, helpFlavourGoldenPath, got, *update)
}
