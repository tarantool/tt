package pack

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alecthomas/participle/v2/lexer/stateful"
	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// DepRelation tags are false-positive highlighted by golang-ci-linter.
// Those tags are needed for participle parser package.
// nolint
type DepRelation struct {
	Relation string `@( "=" "=" | "=" | ">" "=" | "<" "=" | ">" | "<" )`
	Version  string `@Number`
}

// PackDependency tags are false-positive highlighted by golang-ci-linter.
// Those tags are needed for participle parser package.
// nolint
type PackDependency struct {
	Name      string        `@Ident`
	Relations []DepRelation `(@@ ( "," @@ )?)?`
}

type PackDependencies []PackDependency

// createControlDir creates a control directory that contains control file, postinst and preinst.
func createControlDir(cmdCtx cmdcontext.CmdCtx, packCtx PackCtx,
	opts *config.CliOpts, destDirPath string) error {
	log.Debug("Create DEB control file")

	err := os.MkdirAll(destDirPath, dirPermissions)
	if err != nil {
		return err
	}

	version := getVersion(&packCtx, opts, defaultVersion)

	debControlCtx := map[string]interface{}{
		"Name":         packCtx.Name,
		"Version":      version,
		"Maintainer":   defaultMaintainer,
		"Architecture": runtime.GOARCH,
		"Depends":      "",
	}

	deps, err := parseAllDependencies(&cmdCtx, &packCtx)
	if err != nil {
		return err
	}
	addDependenciesDeb(&debControlCtx, deps)

	err = createControlFile(destDirPath, &debControlCtx)
	if err != nil {
		return err
	}

	log.Debugf("Created control file in %s", destDirPath)

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
	err = os.WriteFile(filepath.Join(basePath, "control"), []byte(text), filePermissions)
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

// initScript initializes post- and pre-install script from the passed parameters
// inside the passed directory.
func initScript(destDirPath, scriptName string, mp map[string]interface{}) error {
	var scriptTemplate string
	if scriptName == PostInstScriptName {
		scriptTemplate = postInstScriptContent
	} else if scriptName == PreInstScriptName {
		scriptTemplate = debPreInstScriptContent
	}

	text, err := util.GetTextTemplatedStr(&scriptTemplate, mp)
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath.Join(destDirPath, scriptName), []byte(text), filePermissions)
	if err != nil {
		return err
	}
	return nil
}

//go:embed templates/deb_preinst.sh
var debPreInstScriptContent string

const (
	defaultMaintainer = "Tarantool developer"

	controlFileContent = `Package: {{ .Name }}
Version: {{ .Version }}
Maintainer: {{ .Maintainer }}
Architecture: {{ .Architecture }}
Description: Tarantool environment: {{ .Name }}
Depends: {{ .Depends }}

`
	postInstScriptContent = ``
)
