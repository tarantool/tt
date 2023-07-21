package cmd

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/pack"
)

// packCtx contains information for tt pack command.
var packCtx = &pack.PackCtx{}

func NewPackCmd() *cobra.Command {
	var packCmd = &cobra.Command{Use: "pack TYPE [flags] ..",
		Short: "Pack application into a distributable bundle",
		Long: `Pack application into a distributable bundle

The supported types are: tgz, deb, rpm`,
		ValidArgs: []string{"tgz", "deb", "rpm"},
		Run: func(cmd *cobra.Command, args []string) {
			err := cobra.ExactArgs(1)(cmd, args)
			if err != nil {
				err = fmt.Errorf("incorrect combination of command parameters: %s", err.Error())
				log.Fatalf(err.Error())
			}
			err = cobra.OnlyValidArgs(cmd, args)
			if err != nil {
				err = fmt.Errorf("incorrect combination of command parameters: %s", err.Error())
				log.Fatalf(err.Error())
			}
			if packCtx.CartridgeCompat && args[0] != "tgz" {
				err = fmt.Errorf("cartridge-compat flag can only be used while packing tgz bundle")
				log.Fatalf(err.Error())
			}
			cmdCtx.CommandName = cmd.Name()
			err = modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalPackModule, args)
			handleCmdErr(cmd, err)
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
	packCmd.Flags().BoolVar(&packCtx.WithoutModules, "without-modules",
		packCtx.WithoutModules, "Don't include external modules to the result package")
	packCmd.Flags().BoolVar(&packCtx.WithBinaries, "with-binaries", packCtx.WithoutBinaries,
		"Include tarantool and tt binaries to the result package")
	packCmd.Flags().BoolVar(&packCtx.CartridgeCompat, "cartridge-compat", false,
		"Pack cartridge cli compatible archive (only for tgz type)")

	// TarGZ flags.
	packCmd.Flags().BoolVar(&packCtx.Archive.All, "all", packCtx.Archive.All,
		"Pack all included artifacts")

	// RPMDeb flags.
	packCmd.Flags().StringVar(&packCtx.RpmDeb.PreInst, "preinst", packCtx.RpmDeb.PreInst,
		"preinst file path. Only for for RPM and Deb packing.")
	packCmd.Flags().StringVar(&packCtx.RpmDeb.PostInst, "postinst", packCtx.RpmDeb.PostInst,
		"postinst file path. Only for for RPM and Deb packing.")
	packCmd.Flags().StringVar(&packCtx.RpmDeb.DepsFile, "deps-file", packCtx.RpmDeb.DepsFile,
		"Path to the file that contains dependencies for the RPM and DEB packages")
	packCmd.Flags().BoolVar(&packCtx.RpmDeb.WithTarantoolDeps, "with-tarantool-deps",
		packCtx.RpmDeb.WithTarantoolDeps,
		"Add tarantool and tt as dependencies to the result package")
	packCmd.Flags().StringSliceVar(&packCtx.RpmDeb.Deps, "deps", packCtx.RpmDeb.Deps,
		"Dependencies for the RPM and DEB packages")
	packCmd.Flags().BoolVar(&packCtx.UseDocker, "use-docker",
		packCtx.UseDocker,
		"Use docker for building a package.")

	return packCmd
}

// internalPackModule is a default pack module.
func internalPackModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	err := pack.FillCtx(cmdCtx, packCtx, args)
	if err != nil {
		return err
	}

	checkFlags(packCtx)

	if packCtx.UseDocker {
		return pack.PackInDocker(cmdCtx, packCtx, *cliOpts, os.Args)
	}

	packer := pack.CreatePacker(packCtx)
	if packer == nil {
		return fmt.Errorf("incorrect type of package. Available types: rpm, deb, tgz")
	}

	err = packer.Run(cmdCtx, packCtx, cliOpts)
	if err != nil {
		return fmt.Errorf("failed to pack: %v", err)
	}
	return nil
}

func checkFlags(packCtx *pack.PackCtx) {
	switch pack.PackageType(packCtx.Type) {
	case pack.Tgz:
		if len(packCtx.RpmDeb.Deps) > 0 {
			log.Warnf("You specified the --deps flag," +
				" but you are not packaging RPM or DEB. Flag will be ignored")
		}
		if packCtx.RpmDeb.PreInst != "" {
			log.Warnf("You specified the --preinst flag," +
				" but you are not packaging RPM or DEB. Flag will be ignored")
		}
		if packCtx.RpmDeb.PostInst != "" {
			log.Warnf("You specified the --postinst flag," +
				" but you are not packaging RPM or DEB. Flag will be ignored")
		}
	case pack.Rpm, pack.Deb:
		if packCtx.Archive.All == true {
			log.Warnf("You specified the --all flag," +
				" but you are not packaging a tarball. Flag will be ignored")
		}
	}
}
