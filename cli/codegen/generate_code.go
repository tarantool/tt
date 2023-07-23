package main

import (
	"io/ioutil"
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
			"consoleEvalFuncBody":        "cli/connect/lua/console_eval_func_body.lua",
			"consoleEvalFuncBodyTblsFmt": "cli/connect/lua/console_eval_func_body_tbls_fmt.lua",
			"evalFuncBody":               "cli/connect/lua/eval_func_body.lua",
			"getSuggestionsFuncBody":     "cli/connect/lua/get_suggestions_func_body.lua",
			"getFuncsListInteractOptBody": "cli/connect/lua/" +
				"get_func_list_interactive_opt.lua",
			"getSpaceIndexesListInteractOptBody": "cli/connect/lua/" +
				"get_space_indexes_list_interactive_opt.lua",
			"getSpaceIndexInfoInteractOptBody": "cli/connect/lua/" +
				"get_space_index_info_interactive_opt.lua",
			"getSpacesListInteractOptBody": "cli/connect/lua/" +
				"get_spaces_list_interactive_opt.lua",
			"getSpaceFormatInteractOptBody": "cli/connect/lua/" +
				"get_space_format_interactive_opt.lua",
			"getSpaceInfoInteractOptBody": "cli/connect/lua/" +
				"get_space_info_interactive_opt.lua",
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
			content, err := ioutil.ReadFile(val)
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

	if err = generateLuaCodeVar(); err != nil {
		log.Errorf("error while generating lua code string variables: %s", err)
	}
}
