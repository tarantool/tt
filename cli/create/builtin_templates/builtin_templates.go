package builtin_templates

import "embed"

// TemplatesFs holds the built-in templates, one directory each.
//
// A template lays a project out, and the pair a project runs from lives in
// its hidden .templates/default/ directory. The all: prefix keeps it: a
// directory pattern alone leaves out the files and directories whose names
// begin with a dot.
//
//go:embed all:templates
var TemplatesFs embed.FS

// Names contains built-in template names.
var Names = [...]string{
	"vshard_cluster",
	"single_instance",
	"config_storage",
	"cluster",
}
