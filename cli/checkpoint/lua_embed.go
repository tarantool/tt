package checkpoint

import _ "embed"

//go:embed lua/cat.lua
var catFile string

//go:embed lua/play.lua
var playFile string
