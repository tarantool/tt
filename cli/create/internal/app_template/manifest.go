package app_template

import (
	"fmt"
	"os"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/util"
)

const (
	DefaultManifestName = "MANIFEST.yaml"
)

// UserPrompt describes interactive prompt to get the value of variable from a user.
type UserPrompt struct {
	// Prompt is an input prompt for the variable.
	Prompt string
	// Name is a variable name to store a value to.
	Name string
	// Default is a default value.
	Default string
	// Re is a regular expression for the value validation.
	Re string
}

// TemplateManifest is a manifest for application template.
type TemplateManifest struct {
	// Description is a template description.
	Description string
	// Vars is a set of variables, which values are to be
	// requested from a user.
	Vars []UserPrompt
	// PreHook is a path to the executable to run before template instantiation.
	// Application path is passed as a first parameter.
	PreHook string `mapstructure:"pre-hook"`
	// PostHook is a path to the executable to run after template instantiation.
	// Application path is passed as a first parameter.
	PostHook string `mapstructure:"post-hook"`
	// Include contains a list of files to keep after template instantiaion.
	Include []string
}

func validateManifest(manifest *TemplateManifest) error {
	for _, varInfo := range manifest.Vars {
		if varInfo.Prompt == "" {
			return fmt.Errorf("Missing user prompt.")
		}
		if varInfo.Name == "" {
			return fmt.Errorf("Missing variable name.")
		}
	}
	return nil
}

// LoadManifest loads template manifest from manifestPath.
func LoadManifest(manifestPath string) (TemplateManifest, error) {
	var templateManifest TemplateManifest
	if _, err := os.Stat(manifestPath); err != nil {
		return templateManifest, fmt.Errorf("Failed to get access to manifest file: %s", err)
	}

	rawConfigOpts, err := util.ParseYAML(manifestPath)
	if err != nil {
		return templateManifest, err
	}

	if err := mapstructure.Decode(rawConfigOpts, &templateManifest); err != nil {
		return templateManifest, fmt.Errorf("Failed to decode template manifest: %s", err)
	}

	if err := validateManifest(&templateManifest); err != nil {
		return templateManifest, fmt.Errorf("Invalid manifest format: %s", err)
	}

	return templateManifest, nil
}
