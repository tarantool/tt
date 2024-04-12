package builtin_templates

import (
	"embed"

	"github.com/tarantool/tt/cli/create/builtin_templates/static"
)

//go:embed templates/*
var TemplatesFs embed.FS

// FileModes contains mapping of file modes by built-in template name.
var FileModes = map[string]map[string]int{
	"cartridge":       static.CartridgeFileModes,
	"vshard_cluster":  static.VshardClusterFileModes,
	"single_instance": static.SingleInstanceFileModes,
}

// Names contains built-in template names.
var Names = [...]string{"cartridge", "vshard_cluster", "single_instance"}
