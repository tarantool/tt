package build

import (
	"context"
	"strings"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
)

// checkedFiles returns the files of the stage the file checks of the modules
// check (checkFiles), component by component in the order of components:
// what its layout copied there (laidOut), then the data files its build made
// in its share directory, as results record them - those of a build skipped
// as up to date included, since they are in the stage all the same. A file
// is listed once.
func checkedFiles(
	proj project, stage string, components []string, laidOut map[string][]string,
	results map[string]madeFiles,
) []string {
	var files []string

	for _, name := range components {
		files = append(files, laidOut[name]...)

		data := results[name].Data
		if len(data) == 0 {
			continue
		}

		share := componentShareDir(stage, proj.man.ComponentTarget(name).Namespace)

		files = append(files, joinDir(share, data)...)
	}

	unique := make([]string, 0, len(files))
	seen := make(map[string]bool, len(files))

	for _, file := range files {
		if !seen[file] {
			seen[file] = true
			unique = append(unique, file)
		}
	}

	return unique
}

// checkFiles checks each of files with every check whose Suffix ends its
// name, in the order of files and, for one file, of checks, as the build
// checks the programs a component makes: an error fails the build, and the
// warnings are returned to be shown once all the files passed. What a check
// says is shown as it is, so it names the file itself.
func checkFiles(ctx context.Context, checks []sdkbuild.FileCheck, files []string) (
	[]string, error,
) {
	var warnings []string

	for _, file := range files {
		for _, check := range checks {
			if !strings.HasSuffix(file, check.Suffix) {
				continue
			}

			warning, err := check.Check(ctx, file)
			if err != nil {
				return nil, err
			}

			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}

	return warnings, nil
}
