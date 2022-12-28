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

// Run collects template variables from user in interactive mode.
func (collectTemplateVarsFromUser CollectTemplateVarsFromUser) Run(
	createCtx *create_ctx.CreateCtx, templateCtx *app_template.TemplateCtx) error {
	var err error
	if templateCtx.IsManifestPresent == false {
		return nil
	}

	for _, varInfo := range templateCtx.Manifest.Vars {
		// Check if var is present, and validate it.
		existingValue, found := templateCtx.Vars[varInfo.Name]
		if found {
			if varInfo.Re != "" {
				matched, err := regexp.MatchString(varInfo.Re, existingValue)
				if err != nil {
					return fmt.Errorf("failed to validate user input: %s", err)
				}
				if matched == false {
					if createCtx.SilentMode {
						return fmt.Errorf("invalid format of %s variable", varInfo.Name)
					} else {
						fmt.Printf("Invalid format of %s variable.\n", varInfo.Name)
					}
				} else {
					continue
				}
			} else {
				continue
			}
		}

		matched := false
		var input string
		for matched == false {
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
			if input != "" {
				if varInfo.Re != "" {
					matched, err = regexp.MatchString(varInfo.Re, input)
					if err != nil {
						return fmt.Errorf("failed to validate user input: %s", err)
					}
					if matched == false {
						if createCtx.SilentMode {
							return fmt.Errorf("invalid format of %s variable", varInfo.Name)
						} else {
							fmt.Println("Invalid format. Try again.")
						}
					}
				} else {
					matched = true
				}
			}
		}
		templateCtx.Vars[varInfo.Name] = input
	}

	return nil
}
