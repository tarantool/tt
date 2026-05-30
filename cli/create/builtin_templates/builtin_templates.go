package builtin_templates

import "embed"

//go:embed templates/*
var TemplatesFs embed.FS

// Names contains built-in template names.
var Names = [...]string{
	"vshard_cluster",
	"single_instance",
	"config_storage",
	"cluster",
}
