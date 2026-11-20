// Package smoke is a tt module written outside tt on the public API of the
// SDK alone: a command, a build backend and a check of the files of a
// package. TestOutOfTreeModule builds it on the SDK of the checkout; the
// test writes its go.mod.
package smoke

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3/output"
)

// BackendName is the name of the backend: backend = "smoke" in a build block.
const BackendName = "smoke"

// New is the constructor of the module.
func New(services sdk.Services) sdk.Module {
	return sdk.Module{
		Commands:   []sdk.Mount{{Path: "", Cmd: newSmokeCmd(services)}},
		Backends:   []build.Backend{Backend()},
		FileChecks: []build.FileCheck{{Suffix: ".lua", Check: checkLua}},
	}
}

// greeting is the result of tt smoke greet.
type greeting struct {
	Greeting string `json:"greeting" yaml:"greeting"`
}

// Human prints the greeting.
func (g greeting) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, g.Greeting)

	return err
}

func newSmokeCmd(services sdk.Services) *cobra.Command {
	group := &cobra.Command{
		Use:   "smoke",
		Short: "Check a module built outside tt",
	}

	greet := &cobra.Command{
		Use:   "greet NAME",
		Short: "Greet NAME",
		Args:  cobra.ExactArgs(1),
	}

	format := output.BindFormat(greet.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON)

	greet.RunE = func(_ *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return sdk.Usagef("NAME is empty")
		}

		services.Log().Debug("Greeting", "name", name)

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(greeting{Greeting: "hello, " + name})
	}

	group.AddCommand(greet)

	return group
}

// Config are the fields of a smoke build block.
type Config struct {
	// Text is the string the Lua module the build writes returns. Required.
	Text string `toml:"text"`
}

// Backend returns the backend smoke: it writes a Lua module named after the
// component, which returns the text of the block, as data of the package.
func Backend() build.Backend {
	return build.Backend{
		Name:       BackendName,
		Produces:   []build.Result{build.Data},
		Config:     func() any { return new(Config) },
		New:        build.Typed(newBuild),
		OutputFrom: build.OutputNone,
	}
}

func newBuild(config *Config, component build.Component, _ build.Common) (build.Build, error) {
	if config.Text == "" {
		return nil, build.FieldErrorf("", "text is required for backend %q", BackendName)
	}

	return smokeBuild{text: config.Text, name: component.Name}, nil
}

// smokeBuild is a checked smoke build block.
type smokeBuild struct {
	text string
	name string
}

// Run writes the Lua module into the share directory of the component.
func (b smokeBuild) Run(_ context.Context, c *build.Context) error {
	data := fmt.Sprintf("return %q\n", b.text)

	return os.WriteFile(filepath.Join(c.Dirs.Share, b.name+".lua"), []byte(data), 0o644)
}

// checkLua warns of every Lua file of a package: the warning shows that the
// core ran the checks of the module.
func checkLua(_ context.Context, path string) (string, error) {
	return filepath.Base(path) + " checked by smoke", nil
}
