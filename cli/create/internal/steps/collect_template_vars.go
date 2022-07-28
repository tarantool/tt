package steps

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type CollectTemplateVarsFromUser struct {
}

func (CollectTemplateVarsFromUser) Run(ctx cmdcontext.CreateCtx, templateCtx *templates.TemplateCtx) error {
	var err error
	if templateCtx.IsManifestPresent == false {
		return nil
	}

	for _, varInfo := range templateCtx.Manifest.Vars {
		// Check if var is present, and validate it.
		existingValue, found := templateCtx.Vars[varInfo.Var]
		if found {
			if varInfo.Re != "" {
				matched, err := regexp.MatchString(varInfo.Re, existingValue)
				if err != nil {
					return fmt.Errorf("Failed to validate user input: %s", err)
				}
				if matched == false {
					fmt.Printf("Invalid format of %s variable.\n", varInfo.Var)
				} else {
					continue
				}
			} else {
				continue
			}
		}

		// User input.
		matched := false
		var input string
		reader := bufio.NewReader(os.Stdin)
		for matched == false {
			if varInfo.Default == "" {
				fmt.Printf("%s: ", varInfo.Prompt)
			} else {
				fmt.Printf("%s (default: %s): ", varInfo.Prompt, varInfo.Default)
			}
			input, err = reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("Error getting user input: %s", err)
			}
			input = strings.TrimSuffix(input, "\n")

			if input == "" {
				if varInfo.Default == "" {
					fmt.Println("Please enter a value.")
				} else {
					input = varInfo.Default
					matched = true
				}
			} else {
				if varInfo.Re != "" {
					matched, err = regexp.MatchString(varInfo.Re, input)
					if err != nil {
						return fmt.Errorf("Failed to validate user input: %s", err)
					}
					if matched == false {
						fmt.Println("Invalid format. Try again.")
					}
				} else {
					matched = true
				}
			}
		}
		templateCtx.Vars[varInfo.Var] = input
	}

	return nil
}
