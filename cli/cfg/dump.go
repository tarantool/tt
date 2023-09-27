package cfg

import (
	"fmt"
	"io"
	"os"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"gopkg.in/yaml.v2"
)

// DumpCtx contains information for tt config dump.
type DumpCtx struct {
	// rawDump is a dump mode flag. If set, raw contents of tt configuration file is printed.
	RawDump bool
}

// dumpRaw prints raw content of tt config file.
func dumpRaw(writer io.Writer, cmdCtx *cmdcontext.CmdCtx) error {
	if cmdCtx.Cli.ConfigPath != "" {
		_, err := os.Stat(cmdCtx.Cli.ConfigPath)
		if err != nil {
			return err
		}
		fileContent, err := os.ReadFile(cmdCtx.Cli.ConfigPath)
		if err != nil {
			return err
		}
		writer.Write([]byte(cmdCtx.Cli.ConfigPath + ":\n"))
		writer.Write(fileContent)
	} else {
		return fmt.Errorf("tt configuration file is not found")
	}

	return nil
}

// dumpConfiguration prints tt env configuration with all resolved paths.
func dumpConfiguration(writer io.Writer, cmdCtx *cmdcontext.CmdCtx,
	cliOpts *config.CliOpts) error {
	if cmdCtx.Cli.ConfigPath != "" {
		if _, err := os.Stat(cmdCtx.Cli.ConfigPath); err == nil {
			writer.Write([]byte(cmdCtx.Cli.ConfigPath + ":\n"))
		}
	}
	err := yaml.NewEncoder(writer).Encode(cliOpts)
	return err
}

// RunDump prints tt configuration.
func RunDump(writer io.Writer, cmdCtx *cmdcontext.CmdCtx, dumpCtx *DumpCtx,
	cliOpts *config.CliOpts) error {
	if dumpCtx.RawDump {
		return dumpRaw(writer, cmdCtx)
	}
	return dumpConfiguration(writer, cmdCtx, cliOpts)
}
