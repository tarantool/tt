package pack

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer/stateful"
	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/util"
)

// DepRelation tags are false-positive highlighted by golang-ci-linter.
// Those tags are needed for participle parser package.
//nolint
type DepRelation struct {
	Relation string `@( "=" "=" | "=" | ">" "=" | "<" "=" | ">" | "<" )`
	Version  string `@Number`
}

// PackDependency tags are false-positive highlighted by golang-ci-linter.
// Those tags are needed for participle parser package.
//nolint
type PackDependency struct {
	Name      string        `@Ident`
	Relations []DepRelation `(@@ ( "," @@ )?)?`
}

type PackDependencies []PackDependency

// createControlDir creates a control directory that contains control file, postinst and preinst.
func createControlDir(packCtx *PackCtx, destDirPath string) error {
	log.Debugf("Create DEB control file")
	err := os.MkdirAll(destDirPath, dirPermissions)
	if err != nil {
		return err
	}

	name := packCtx.Name
	if packCtx.Name == "" {
		name = "bundle"
	}
	version := getVersion(packCtx)

	debControlCtx := map[string]interface{}{
		"Name":         name,
		"Version":      version,
		"Maintainer":   defaultMaintainer,
		"Architecture": runtime.GOARCH,
		"Depends":      "",
	}

	deps, err := parseDependencies(packCtx.RpmDeb.Deps)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		addDependenciesDeb(&debControlCtx, deps)
	}

	fileDeps, err := parseDependenciesFromFile(packCtx.RpmDeb.Deps, packCtx.RpmDeb.DepsFile)
	if err != nil {
		return err
	}
	if len(fileDeps) > 0 {
		addDependenciesDeb(&debControlCtx, fileDeps)
	}

	if packCtx.RpmDeb.WithTarantoolDeps {
		ttBinDeps, err := getTntTTVersions(packCtx)
		if err != nil {
			return err
		}
		deps = append(deps, ttBinDeps...)
	}

	err = createControlFile(destDirPath, &debControlCtx)
	if err != nil {
		return err
	}
	log.Infof("Created control in %s", destDirPath)

	// Add postinst and preinst scripts step.
	if packCtx.RpmDeb.PreInst == "" {
		err = initScript(destDirPath, PreInstScriptName, map[string]interface{}{})
		if err != nil {
			return err
		}
	} else {
		err = copy.Copy(packCtx.RpmDeb.PreInst,
			filepath.Join(destDirPath, PreInstScriptName))
		if err != nil {
			return err
		}
	}
	if packCtx.RpmDeb.PostInst == "" {
		err = initScript(destDirPath, PostInstScriptName, map[string]interface{}{})
		if err != nil {
			return err
		}
	} else {
		err = copy.Copy(packCtx.RpmDeb.PostInst,
			filepath.Join(destDirPath, PostInstScriptName))
		if err != nil {
			return err
		}
	}

	return nil
}

// getDebRelation returns a correct relation string from the passed one.
func getDebRelation(relation string) string {
	if relation == ">" || relation == "<" {
		// Deb format uses >> and << instead of > and <
		return fmt.Sprintf("%s%s", relation, relation)
	} else if relation == "==" {
		return "="
	}

	return relation
}

// addDependenciesDeb adds parsed dependencies to the passed map.
func addDependenciesDeb(debControlCtx *map[string]interface{}, deps PackDependencies) {
	var depsList []string

	for _, dep := range deps {
		for _, r := range dep.Relations {
			depsList = append(depsList, fmt.Sprintf("%s (%s %s)",
				dep.Name, getDebRelation(r.Relation), r.Version))
		}

		if len(dep.Relations) == 0 {
			depsList = append(depsList, dep.Name)
		}
	}

	(*debControlCtx)["Depends"] = strings.Join(depsList, ", ")
}

// createControlFile creates a control file from template.
func createControlFile(basePath string, debControlCtx *map[string]interface{}) error {
	controlTempl := controlFileContent
	text, err := util.GetTextTemplatedStr(&controlTempl, debControlCtx)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(basePath, "control"), []byte(text), filePermissions)
	if err != nil {
		return err
	}
	return nil
}

func getLexer() *stateful.Definition {
	return stateful.MustSimple([]stateful.Rule{
		{
			Name:    "Comment",
			Pattern: `(?:#|//)[^\n]*\n?`,
			Action:  nil,
		},
		{
			Name:    "Ident",
			Pattern: `[a-zA-Z]\w*`,
			Action:  nil,
		},
		{
			Name:    "Number",
			Pattern: `(\d+\.?)+`,
			Action:  nil,
		},
		{
			Name:    "Punct",
			Pattern: `[-[!@#$%^&*()+_={}\|:;"'<,>.?/]|]`,
			Action:  nil,
		},
		{
			Name:    "Whitespace",
			Pattern: `[ \t\n\r]+`,
			Action:  nil,
		},
	})
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
				fmt.Errorf("Error during parse dependencies file: %s. Trying to parse: %s",
					err, dep)
		}

		deps = append(deps, parsedDep)
	}

	return deps, nil
}

// parseDependenciesFromFile parses all dependencies from passed file.
func parseDependenciesFromFile(deps []string, depsFile string) (PackDependencies, error) {
	var err error

	if depsFile != "" && len(deps) != 0 {
		return nil, fmt.Errorf("You can't specify --deps and --deps-file flags at the same time")
	}

	if depsFile == "" && len(deps) == 0 {
		return nil, nil
	}

	if depsFile != "" {
		if _, err := os.Stat(depsFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("Invalid path to file with dependencies: %s", err)
		}

		content, err := util.GetFileContent(depsFile)
		if err != nil {
			return nil, fmt.Errorf("Failed to get file content: %s", err)
		}

		deps = strings.Split(content, "\n")
	}

	parsedDeps, err := parseDependencies(deps)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse dependencies file: %s", err)
	}

	return parsedDeps, nil
}

// initScript initializes post- and pre-install script from the passed parameters
// inside the passed directory.
func initScript(destDirPath, scriptName string, mp map[string]interface{}) error {
	var scriptTemplate string
	if scriptName == PostInstScriptName {
		scriptTemplate = PostInstScriptContent
	} else if scriptName == PreInstScriptName {
		scriptTemplate = PreInstScriptContent
	}

	text, err := util.GetTextTemplatedStr(&scriptTemplate, mp)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(destDirPath, scriptName), []byte(text), filePermissions)
	if err != nil {
		return err
	}
	return nil
}

const (
	defaultMaintainer = "Tarantool developer"

	controlFileContent = `Package: {{ .Name }}
Version: {{ .Version }}
Maintainer: {{ .Maintainer }}
Architecture: {{ .Architecture }}
Description: Tarantool environment: {{ .Name }}
Depends: {{ .Depends }}

`
	PreInstScriptContent = `/bin/sh -c 'groupadd -r tarantool > /dev/null 2>&1 || :'
/bin/sh -c 'useradd -M -N -g tarantool -r -d /var/lib/tarantool -s /sbin/nologin \
    -c "Tarantool Server" tarantool > /dev/null 2>&1 || :'
/bin/sh -c 'mkdir -p /etc/tarantool/conf.d/ --mode 755 2>&1 || :'
/bin/sh -c 'mkdir -p /var/lib/tarantool/ --mode 755 2>&1 || :'
/bin/sh -c 'chown tarantool:tarantool /var/lib/tarantool 2>&1 || :'
/bin/sh -c 'mkdir -p /var/run/tarantool/ --mode 755 2>&1 || :'
/bin/sh -c 'chown tarantool:tarantool /var/run/tarantool 2>&1 || :'
`

	PostInstScriptContent = ``
)
