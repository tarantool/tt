package app_template

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errMissingUserPrompt   = errors.New("missing user prompt")
	errMissingVariableName = errors.New("missing variable name")
	errUnknownVarType      = errors.New("unknown type")
	errBoundsOfString      = errors.New(`min and max need type "integer"`)
	errMinAboveMax         = errors.New("min is greater than max")
)

// InvalidValueError tells why a value of a template variable is refused.
type InvalidValueError struct {
	// Value is the refused value.
	Value string
	// Reason follows the quoted value: "is not an integer >= 2".
	Reason string
}

// Error implements the error interface: `"3abc" is not an integer >= 2`.
func (err *InvalidValueError) Error() string {
	return fmt.Sprintf("%q %s", err.Value, err.Reason)
}

const (
	DefaultManifestName = "MANIFEST.yaml"
)

// UserPrompt describes interactive prompt to get the value of variable from a user.
type UserPrompt struct {
	// Prompt is an input prompt for the variable.
	Prompt string `mapstructure:"prompt"`
	// Name is a variable name to store a value to.
	Name string `mapstructure:"name"`
	// Default is a default value.
	Default string `mapstructure:"default"`
	// Re is a regular expression for the value validation.
	Re string `mapstructure:"re"`
	// Type is the type of the value: VarTypeString (the default, also
	// given by an empty Type) or VarTypeInteger.
	Type string `mapstructure:"type"`
	// Min is the least value an integer variable takes, if set.
	Min *int `mapstructure:"min"`
	// Max is the greatest value an integer variable takes, if set.
	Max *int `mapstructure:"max"`
}

const (
	// VarTypeString is the type of a variable that takes any text.
	VarTypeString = "string"
	// VarTypeInteger is the type of a variable that takes a decimal integer.
	VarTypeInteger = "integer"
)

// Validate checks a value of the variable against its type, bounds and
// regular expression. The error says what the value should look like:
// `"3abc" is not an integer >= 2`.
func (prompt UserPrompt) Validate(value string) error {
	if prompt.Type == VarTypeInteger {
		number, err := strconv.Atoi(value)
		if err != nil || (prompt.Min != nil && number < *prompt.Min) ||
			(prompt.Max != nil && number > *prompt.Max) {
			return &InvalidValueError{Value: value, Reason: "is not " + prompt.integerText()}
		}
	}

	if prompt.Re == "" {
		return nil
	}

	matched, err := regexp.MatchString(prompt.Re, value)
	if err != nil {
		return fmt.Errorf("the regular expression %q of the template is invalid: %w",
			prompt.Re, err)
	}

	if !matched {
		return &InvalidValueError{Value: value, Reason: "does not match " + prompt.Re}
	}

	return nil
}

// integerText names the values an integer variable takes, with its bounds:
// "an integer >= 2".
func (prompt UserPrompt) integerText() string {
	switch {
	case prompt.Min != nil && prompt.Max != nil:
		return fmt.Sprintf("an integer from %d to %d", *prompt.Min, *prompt.Max)
	case prompt.Min != nil:
		return fmt.Sprintf("an integer >= %d", *prompt.Min)
	case prompt.Max != nil:
		return fmt.Sprintf("an integer <= %d", *prompt.Max)
	default:
		return "an integer"
	}
}

// TemplateManifest is a manifest for application template.
type TemplateManifest struct {
	// Description is a template description.
	Description string `mapstructure:"description"`
	// Vars is a set of variables, which values are to be
	// requested from a user.
	Vars []UserPrompt `mapstructure:"vars"`
	// PreHook is a path to the executable to run before template instantiation.
	// Application path is passed as a first parameter.
	PreHook string `mapstructure:"pre-hook"`
	// PostHook is a path to the executable to run after template instantiation.
	// Application path is passed as a first parameter.
	PostHook string `mapstructure:"post-hook"`
	// Include contains a list of files to keep after template instantiation.
	Include []string `mapstructure:"include"`
	// FollowUpMessage is a message to print to console after application creation.
	FollowUpMessage string `mapstructure:"follow-up-message"`
}

func validateManifest(manifest *TemplateManifest) error {
	for _, varInfo := range manifest.Vars {
		if varInfo.Prompt == "" {
			return errMissingUserPrompt
		}

		if varInfo.Name == "" {
			return errMissingVariableName
		}

		switch varInfo.Type {
		case "", VarTypeString:
			if varInfo.Min != nil || varInfo.Max != nil {
				return fmt.Errorf("%s: %w", varInfo.Name, errBoundsOfString)
			}
		case VarTypeInteger:
			if varInfo.Min != nil && varInfo.Max != nil && *varInfo.Min > *varInfo.Max {
				return fmt.Errorf("%s: %w: %d > %d",
					varInfo.Name, errMinAboveMax, *varInfo.Min, *varInfo.Max)
			}
		default:
			return fmt.Errorf("%s: %w %q, expected %q or %q", varInfo.Name,
				errUnknownVarType, varInfo.Type, VarTypeString, VarTypeInteger)
		}

		_, err := regexp.Compile(varInfo.Re)
		if err != nil {
			return fmt.Errorf("%s: invalid regular expression: %w", varInfo.Name, err)
		}
	}

	return nil
}

// LoadManifest loads template manifest from manifestPath.
func LoadManifest(manifestPath string) (TemplateManifest, error) {
	var templateManifest TemplateManifest

	_, err := os.Stat(manifestPath)
	if err != nil {
		return templateManifest, fmt.Errorf("failed to get access to manifest file: %w", err)
	}

	rawConfigOpts, err := util.ParseYAML(manifestPath)
	if err != nil {
		return templateManifest, err
	}

	err = mapstructure.Decode(rawConfigOpts, &templateManifest)
	if err != nil {
		return templateManifest, fmt.Errorf("failed to decode template manifest: %w", err)
	}

	err = validateManifest(&templateManifest)
	if err != nil {
		return templateManifest, fmt.Errorf("invalid manifest format: %w", err)
	}

	return templateManifest, nil
}
