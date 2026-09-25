package core_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// corePath is the import path of this package; its internal packages are
// under it.
const corePath = "github.com/tarantool/tt/v3/core"

// mainPath is the import path of tt's main package, the one package under
// cli/ that builds tt from the core.
const mainPath = "github.com/tarantool/tt/v3/cli"

// TestCLIDoesNotImportCore checks that no package under cli/ but tt's main
// package, tests included, depends on the core, directly or through another
// package: the core is built on top of the commands.
func TestCLIDoesNotImportCore(t *testing.T) {
	t.Parallel()

	// One line per package and per test of a package: its path, then every
	// package it depends on.
	list := exec.CommandContext(t.Context(), "go", "list", "-test",
		"-f", "{{.ImportPath}}{{range .Deps}} {{.}}{{end}}", "../cli/...")

	out, err := list.Output()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Greater(t, len(lines), 50, "go list must reach the packages under cli/")

	for _, line := range lines {
		fields := strings.Fields(line)
		if fields[0] == mainPath {
			continue
		}

		for _, dep := range fields[1:] {
			assert.False(t, dep == corePath || strings.HasPrefix(dep, corePath+"/"),
				"%s depends on %s", fields[0], dep)
		}
	}
}
