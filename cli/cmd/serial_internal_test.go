package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoParallelTests checks that no test of the package calls t.Parallel.
//
// The commands of tt bind their flags to the variables of this package,
// cmdCtx and the options of every command: building the command tree writes
// the defaults of the flags into them, and running it the values parsed. Two
// tests that build or run commands at once race on these variables, and the
// race detector fails with them every other test running at that moment. So
// the tests of the package run one at a time, until the commands keep their
// state out of package variables.
func TestNoParallelTests(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	fset := token.NewFileSet()

	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		require.NoError(t, err)

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			if fun, ok := call.Fun.(*ast.SelectorExpr); ok && fun.Sel.Name == "Parallel" {
				t.Errorf("%s: a test of package cmd calls Parallel; the tests of the "+
					"package share the variables the command tree is bound to and "+
					"run one at a time: drop the call", fset.Position(call.Pos()))
			}

			return true
		})
	}
}
