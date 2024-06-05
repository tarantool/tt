package main

import (
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/dave/jennifer/jen"
)

type generateLuaCodeOpts struct {
	PackageName  string
	FileName     string
	PackagePath  string
	VariablesMap map[string]string
}

var luaCodeFiles = []generateLuaCodeOpts{
	{
		PackageName: "running",
		FileName:    "cli/running/lua_code_gen.go",
		VariablesMap: map[string]string{
			"checkSyntax": "cli/running/lua/check.lua",
		},
	},
	{
		PackageName: "connect",
		FileName:    "cli/connect/lua_code_gen.go",
		VariablesMap: map[string]string{
			"evalFuncBody":           "cli/connect/lua/eval_func_body.lua",
			"getSuggestionsFuncBody": "cli/connect/lua/get_suggestions_func_body.lua",
		},
	},
	{
		PackageName: "connector",
		FileName:    "cli/connector/lua_code_gen.go",
		VariablesMap: map[string]string{
			"callFuncTmpl": "cli/connector/lua/call_func_template.lua",
			"evalFuncTmpl": "cli/connector/lua/eval_func_template.lua",
		},
	},
	{
		PackageName: "checkpoint",
		FileName:    "cli/checkpoint/lua_code_gen.go",
		VariablesMap: map[string]string{
			"catFile":  "cli/checkpoint/lua/cat.lua",
			"playFile": "cli/checkpoint/lua/play.lua",
		},
	},
}

func generateLuaCodeVar() error {
	for _, opts := range luaCodeFiles {
		f := jen.NewFile(opts.PackageName)
		f.Comment("This file is generated! DO NOT EDIT\n")

		for key, val := range opts.VariablesMap {
			content, err := os.ReadFile(val)
			if err != nil {
				return err
			}

			f.Var().Id(key).Op("=").Lit(string(content))
		}

		f.Save(opts.FileName)
	}

	return nil
}

// generateFileModeFile generates
// var FileModes = map[string]int {
// "filename": filemode,
// }
func generateFileModeFile(path string, filename string, varNamePrefix string) error {
	goFile := jen.NewFile("static")
	goFile.Comment("This file is generated! DO NOT EDIT\n")

	fileModeMap, err := getFileModes(path)
	if err != nil {
		return err
	}

	varName := varNamePrefix + "FileModes"
	goFile.Var().Id(varName).Op("=").Map(jen.String()).Int().Values(jen.DictFunc(func(d jen.Dict) {
		for key, element := range fileModeMap {
			d[jen.Lit(key)] = jen.Lit(element).Commentf("/* %#o */", element)
		}
	}))

	return goFile.Save(filename)
}

// getFileModes return map with relative file names and modes.
func getFileModes(root string) (map[string]int, error) {
	fileModeMap := make(map[string]int)

	err := filepath.Walk(root, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !fileInfo.IsDir() {
			rel, err := filepath.Rel(root, filePath)

			if err != nil {
				return err
			}

			fileModeMap[rel] = int(fileInfo.Mode())
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return fileModeMap, nil
}

func main() {
	err := generateFileModeFile(
		"cli/create/builtin_templates/templates/cartridge",
		"cli/create/builtin_templates/static/cartridge_template_filemodes_gen.go",
		"Cartridge",
	)
	if err != nil {
		log.Errorf("error while generating file modes: %s", err)
	}
	err = generateFileModeFile(
		"cli/create/builtin_templates/templates/vshard_cluster",
		"cli/create/builtin_templates/static/vshard_cluster_template_filemodes_gen.go",
		"VshardCluster",
	)
	if err != nil {
		log.Errorf("error while generating file modes: %s", err)
	}
	err = generateFileModeFile(
		"cli/create/builtin_templates/templates/single_instance",
		"cli/create/builtin_templates/static/single_instance_template_filemodes_gen.go",
		"SingleInstance",
	)
	if err != nil {
		log.Errorf("error while generating file modes: %s", err)
	}

	if err = generateLuaCodeVar(); err != nil {
		log.Errorf("error while generating lua code string variables: %s", err)
	}
}
