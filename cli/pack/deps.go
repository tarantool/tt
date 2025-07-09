package pack

import (
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
)

// parseAllDependencies collects all information about expected dependencies
// from the passed contexts. It parses dependencies file, dependencies list,
// collects tarantool and tt cli as dependencies and returns the result slice
// of PackDependencies.
func parseAllDependencies(cmdCtx *cmdcontext.CmdCtx,
	packCtx *PackCtx,
) (PackDependencies, error) {
	var deps PackDependencies
	var err error

	if len(packCtx.RpmDeb.Deps) > 0 {
		deps, err = parseDependencies(packCtx.RpmDeb.Deps)
		if err != nil {
			return nil, err
		}
	}

	if packCtx.RpmDeb.DepsFile != "" {
		fileDeps, err := parseDependenciesFromFile(packCtx.RpmDeb.DepsFile)
		if err != nil {
			return nil, err
		}
		deps = append(deps, fileDeps...)
	}

	if packCtx.RpmDeb.WithTarantoolDeps {
		ttBinDeps, err := getTntTtAsDeps(cmdCtx)
		if err != nil {
			return nil, err
		}
		deps = append(deps, ttBinDeps...)
	}
	return deps, nil
}

// parseDependencies accepts a slice of strings and parses dependencies from it.
func parseDependencies(rawDeps []string) (PackDependencies, error) {
	parser := participle.MustBuild(
		&PackDependency{},
		participle.Lexer(getLexer()),
		participle.Elide("Comment", "Whitespace"),
	)

	deps := PackDependencies{}
	for _, dep := range rawDeps {
		dep = strings.TrimSpace(dep)

		// Skip empty lines and comments.
		if dep == "" || strings.HasPrefix(dep, "//") {
			continue
		}

		parsedDep := PackDependency{}

		if err := parser.ParseString("", dep, &parsedDep); err != nil {
			return nil,
				fmt.Errorf("error during parse dependencies file: %s. Trying to parse: %s",
					err, dep)
		}

		deps = append(deps, parsedDep)
	}

	return deps, nil
}

// parseDependenciesFromFile parses all dependencies from passed file.
func parseDependenciesFromFile(depsFile string) (PackDependencies, error) {
	var err error
	var deps []string

	if _, err := os.Stat(depsFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("invalid path to file with dependencies: %s", err)
	}

	content, err := util.GetFileContent(depsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to get file content: %s", err)
	}

	deps = strings.Split(content, "\n")

	parsedDeps, err := parseDependencies(deps)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dependencies file: %s", err)
	}

	return parsedDeps, nil
}
