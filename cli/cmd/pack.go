package cmd

import (
	"fmt"
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/pack"
)

// packCtx contains information for tt pack command.
var packCtx = &cmdcontext.PackCtx{}

func NewPackCmd() *cobra.Command {
	var packCmd = &cobra.Command{Use: "pack TYPE [flags] ..",
		Short: "Packs application into a distributable bundle",
		Run: func(cmd *cobra.Command, args []string) {
			err := cobra.ExactArgs(1)(cmd, args)
			if err != nil {
				err = fmt.Errorf("Incorrect combination of command parameters: %s", err.Error())
				log.Fatalf(err.Error())
			}
			cmdCtx.CommandName = cmd.Name()
			err = modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalPackModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	// Common flags.
	packCmd.Flags().StringVar(&packCtx.Name, "name", packCtx.Name,
		"Package name")
	packCmd.Flags().StringVar(&packCtx.Version, "version", packCtx.Version,
		"Package version")
	packCmd.Flags().StringSliceVar(&packCtx.AppList, "app-list", packCtx.AppList,
		"List of applications for packaging")
	packCmd.Flags().StringVar(&packCtx.FileName, "filename", packCtx.FileName,
		"Explicitly set filename of the bundle")
	packCmd.Flags().BoolVar(&packCtx.WithoutBinaries, "without-binaries",
		packCtx.WithoutBinaries, "Don't include tarantool and tt binaries to the result package")
	packCmd.Flags().BoolVar(&packCtx.WithBinaries, "with-binaries", packCtx.WithoutBinaries,
		"Include tarantool and tt binaries to the result package")

	// TarGZ flags.
	packCmd.Flags().BoolVar(&packCtx.Archive.All, "all", packCtx.Archive.All,
		"Pack all included artifacts")

	return packCmd
}

// internalPackModule is a default pack module.
func internalPackModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	log.Debugf("Config path is located here: %s", cmdCtx.Cli.ConfigPath)

	opts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	err = pack.FillCtx(cmdCtx, opts, packCtx, args)
	if err != nil {
		return err
	}

	packer := pack.CreatePacker(&cmdCtx.Pack)
	if packer == nil {
		return fmt.Errorf("Incorrect type of package")
	}

	err = packer.Run(cmdCtx)
	if err != nil {
		return fmt.Errorf("Failed to pack: %v", err)
	}
	return nil
}
