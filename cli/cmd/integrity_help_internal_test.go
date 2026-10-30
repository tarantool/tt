package cmd

import (
	"testing"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmd/helpgolden"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// helpIntegrityGoldenPath is the golden file TestHelpIntegrityGolden compares
// against.
const helpIntegrityGoldenPath = "testdata/help_integrity.golden"

// TestHelpIntegrityGolden pins the help of a distribution that checks
// integrity with the RSA provider, as tt-ee does: the flags that enable the
// checks at the root, how often the watchdog repeats them for tt instance
// start, and the key that signs what a command publishes.
//
//nolint:paralleltest // Replaces the package root, the flavour, the provider and os.Stdout.
func TestHelpIntegrityGolden(t *testing.T) {
	previousProvider := integrityprovider.Get()

	t.Cleanup(func() { integrityprovider.Set(previousProvider) })

	integrityprovider.Set(integrity.NewRSAProvider())

	got := helpgolden.RenderHelp(t, newFlavourRoot,
		"The help of a distribution named \"Tarantool CLI EE\" that checks\n"+
			"integrity with the RSA provider. Regenerate with:\n"+
			"  go test ./cli/cmd -run TestHelpIntegrityGolden -update",
		nil,
		[]string{"instance", "start"},
		[]string{"cluster", "publish"},
		[]string{"cluster", "replicaset", "promote"},
		[]string{"replicaset", "vshard", "bootstrap"})

	helpgolden.Compare(t, helpIntegrityGoldenPath, got, *update)
}
