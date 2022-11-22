package steps

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// Reader interface is used for reading user input.
type Reader interface {
	// readLine reads a single line of text and returns it.
	readLine() (string, error)
}

// consoleReader implements reading from console.
type consoleReader struct {
	stdinReader *bufio.Reader
}

// readLine reads a single line from the console. New-line symbol is trimmed.
func (consoleReader consoleReader) readLine() (string, error) {
	input, err := consoleReader.stdinReader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("error getting user input: %s", err)
	}
	return strings.TrimSuffix(input, "\n"), nil
}

// NewConsoleReader creates new console reader.
func NewConsoleReader() consoleReader {
	return consoleReader{bufio.NewReader(os.Stdin)}
}

// CollectTemplateVarsFromUser represents interactive variables collecting step.
type CollectTemplateVarsFromUser struct {
	// Reader is used to get user input.
	Reader Reader
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
				input, err = collectTemplateVarsFromUser.Reader.readLine()
				if err != nil {
					return fmt.Errorf("error reading user input: %s", err)
				}
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
