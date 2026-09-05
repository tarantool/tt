package steps

import (
	"fmt"
	"regexp"
	"strings"

	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// stringReader is the interface that wraps the ReadString method.
type stringReader interface {
	ReadString(delim byte) (line string, err error)
}

// CollectTemplateVarsFromUser represents interactive variables collecting step.
type CollectTemplateVarsFromUser struct {
	// Reader is used to get user input.
	Reader stringReader
}

func validateExistingValue(createCtx *create_ctx.CreateCtx, varInfo app_template.UserPrompt,
	existingValue string, found bool,
) (bool, error) {
	if !found || varInfo.Re == "" {
		return found, nil
	}

	matched, err := regexp.MatchString(varInfo.Re, existingValue)
	if err != nil {
		return false, fmt.Errorf("failed to validate user input: %s", err)
	}
	if matched {
		return true, nil
	}
	if createCtx.SilentMode {
		return false, fmt.Errorf("invalid format of %s variable", varInfo.Name)
	}
	fmt.Printf("Invalid format of %s variable.\n", varInfo.Name)
	return false, nil
}

// Run collects template variables from user in interactive mode.
func (collectTemplateVarsFromUser CollectTemplateVarsFromUser) Run(
	createCtx *create_ctx.CreateCtx, templateCtx *app_template.TemplateCtx,
) error {
	if !templateCtx.IsManifestPresent {
		return nil
	}

	for _, varInfo := range templateCtx.Manifest.Vars {
		// Check if var is present, and validate it.
		existingValue, found := templateCtx.Vars[varInfo.Name]
		valid, err := validateExistingValue(createCtx, varInfo, existingValue, found)
		if err != nil {
			return err
		}
		if valid {
			continue
		}

		matched := false
		var input string
		for !matched {
			if varInfo.Default == "" {
				if createCtx.SilentMode {
					return fmt.Errorf("%s variable value is not set", varInfo.Name)
				}
				fmt.Printf("%s: ", varInfo.Prompt)
			} else {
				if createCtx.SilentMode {
					input = varInfo.Default
				} else {
					fmt.Printf("%s (default: %s): ", varInfo.Prompt, varInfo.Default)
				}
			}

			// User input.
			if !createCtx.SilentMode {
				if input, err = collectTemplateVarsFromUser.Reader.ReadString('\n'); err != nil {
					return fmt.Errorf("error reading user input: %s", err)
				}
				input = strings.TrimSuffix(input, "\n")
			}

			if input == "" {
				if varInfo.Default == "" {
					fmt.Println("Please enter a value.")
				} else {
					input = varInfo.Default
				}
			}
			if input == "" {
				continue
			}
			if varInfo.Re == "" {
				matched = true
				continue
			}
			matched, err = regexp.MatchString(varInfo.Re, input)
			if err != nil {
				return fmt.Errorf("failed to validate user input: %s", err)
			}
			if !matched {
				if createCtx.SilentMode {
					return fmt.Errorf("invalid format of %s variable", varInfo.Name)
				}
				fmt.Println("Invalid format. Try again.")
			}
		}
		templateCtx.Vars[varInfo.Name] = input
	}

	return nil
}
