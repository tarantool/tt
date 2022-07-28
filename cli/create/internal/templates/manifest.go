package templates

import (
	"fmt"
	"os"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/util"
)

type UserPrompt struct {
	// Prompt is an input prompt for the variable.
	Prompt string `mapstructure:"prompt"`
	// Var is a variable name to store a value to.
	Var string `mapstructure:"var"`
	// Default is a default value.
	Default string `mapstructure:"default"`
	// Re is a regular expression for the value validation.
	Re string `mapstructure:"re"`
}

type TemplateManifest struct {
	// Description is a template description.
	Description string `mapstructure:"description"`
	// Vars is a set of variables, which values are to be
	// requested from a user.
	Vars []UserPrompt
	// PreHook is a path to the executable to run before template instantiation.
	// Application path is passed as a first parameter.
	PreHook string `mapstructure:"pre-hook"`
	// PreHook is a path to the executable to run after template instantiation.
	// Application path is passed as a first parameter.
	PostHook string `mapstructure:"post-hook"`
}

// LoadManifest loads template manifest from manifestPath.
func LoadManifest(manifestPath string) (TemplateManifest, error) {
	var templateManifest TemplateManifest
	if _, err := os.Stat(manifestPath); err != nil {
		return templateManifest, fmt.Errorf("Failed to get access to manifest file: %s", err)
	}

	rawConfigOpts, err := util.ParseYAML(manifestPath)
	if err != nil {
		return templateManifest, fmt.Errorf("Failed to parse template manifest: %s", err)
	}

	if err := mapstructure.Decode(rawConfigOpts, &templateManifest); err != nil {
		return templateManifest, fmt.Errorf("Failed to parse template manifest: %s", err)
	}

	return templateManifest, nil
}
