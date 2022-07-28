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

// Reader interface is used from reading user input.
type Reader interface {
	readLine() (string, error)
}

// consoleReader implements reading from console.
type consoleReader struct {
	stdinReader *bufio.Reader
}

// readLine reads line from console. New-line symbol is trimmed.
func (consoleReader consoleReader) readLine() (string, error) {
	input, err := consoleReader.stdinReader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("Error getting user input: %s", err)
	}
	return strings.TrimSuffix(input, "\n"), nil
}

// NewConsoleReader create new console reader.
func NewConsoleReader() consoleReader {
	return consoleReader{bufio.NewReader(os.Stdin)}
}

type CollectTemplateVarsFromUser struct {
	// Reader is used to get user input.
	Reader Reader
}

// Run collects template variables from user in interactive mode.
func (collectTemplateVarsFromUser CollectTemplateVarsFromUser) Run(ctx cmdcontext.CreateCtx,
	templateCtx *templates.TemplateCtx) error {
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
					if ctx.SilentMode {
						return fmt.Errorf("Invalid format of %s variable.", varInfo.Var)
					} else {
						fmt.Printf("Invalid format of %s variable.\n", varInfo.Var)
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
				if ctx.SilentMode {
					return fmt.Errorf("%s variable value is not set.", varInfo.Var)
				}
				fmt.Printf("%s: ", varInfo.Prompt)
			} else {
				if ctx.SilentMode {
					input = varInfo.Default
				} else {
					fmt.Printf("%s (default: %s): ", varInfo.Prompt, varInfo.Default)
				}
			}

			// User input.
			if !ctx.SilentMode {
				input, err = collectTemplateVarsFromUser.Reader.readLine()
				if err != nil {
					return fmt.Errorf("Error reading user input: %s", err)
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
						return fmt.Errorf("Failed to validate user input: %s", err)
					}
					if matched == false {
						if ctx.SilentMode {
							return fmt.Errorf("Invalid format of %s variable.", varInfo.Var)
						} else {
							fmt.Println("Invalid format. Try again.")
						}
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
