package connector

import _ "embed"

//go:embed lua/eval_func_template.lua
var evalFuncTmpl string
