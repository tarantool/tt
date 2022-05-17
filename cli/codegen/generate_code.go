package main

import (
	"io/ioutil"

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
			"instanceLauncher": "cli/running/lua/launcher.lua",
			"checkSyntax":      "cli/running/lua/check.lua",
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
			"catFile": "cli/checkpoint/lua/cat.lua",
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

func main() {
	if err := generateLuaCodeVar(); err != nil {
		log.Errorf("Error while generating lua code string variables: %s", err)
	}
}
