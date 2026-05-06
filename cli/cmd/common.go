package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/integrity"
)

// errNoConfig is returned if environment config file tt.yaml not found.
var errNoConfig = errors.New(configure.ConfigName +
	" not found, you need to create tt environment config with 'tt init'" +
	" or provide exact config location with --cfg option")

// isConfigExist returns `true` if environment config file tt.yaml exist.
func isConfigExist(cmdCtx *cmdcontext.CmdCtx) bool {
	return cmdCtx.Cli.ConfigPath != ""
}

// createDataCollectors creates data collectors factory based on the integrity context.
func createDataCollectors(ctx integrity.IntegrityCtx) (libcluster.DataCollectorFactory, error) {
	var collectors libcluster.DataCollectorFactory
	hashers, verifiers, err := integrity.GetStorageVerifiers(ctx)
	if errors.Is(err, integrity.ErrNotConfigured) {
		collectors = libcluster.NewDataCollectorFactory()
	} else if err != nil {
		return nil, fmt.Errorf("failed to create collectors with integrity check: %w", err)
	} else {
		collectors = libcluster.NewDataCollectorFactory(
			libcluster.WithFileReadFunc(func(path string) (io.ReadCloser, error) {
				return cmdCtx.Integrity.Repository.Read(path)
			}),
			libcluster.WithIntegrity(libcluster.IntegrityOptions{
				Hashers:   hashers,
				Verifiers: verifiers,
			}),
		)
	}
	return collectors, nil
}

// createDataPublishers creates data publishers factory based on the the private key.
func createDataPublishers(privateKey string) (libcluster.DataPublisherFactory, error) {
	var publishers libcluster.DataPublisherFactory
	if privateKey != "" {
		hashers, signerVerifiers, err := integrity.GetStorageSigners(privateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create publishers with integrity: %w", err)
		}
		publishers = libcluster.NewDataPublisherFactory(
			libcluster.WithIntegrity(libcluster.IntegrityOptions{
				Hashers:         hashers,
				SignerVerifiers: signerVerifiers,
			}),
		)
	} else {
		publishers = libcluster.NewDataPublisherFactory()
	}
	return publishers, nil
}

// createDataCollectorsAndDataPublishers combines data collectors and publishers creating.
func createDataCollectorsAndDataPublishers(ctx integrity.IntegrityCtx,
	privateKey string,
) (libcluster.DataCollectorFactory, libcluster.DataPublisherFactory, error) {
	collectors, err := createDataCollectors(ctx)
	if err != nil {
		return nil, nil, err
	}
	publishers, err := createDataPublishers(privateKey)
	if err != nil {
		return nil, nil, err
	}
	return collectors, publishers, err
}

func RunModuleFunc(internalModule modules.InternalFunc) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		cmdCtx.CommandName = cmd.Name()
		err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalModule, args)
		if err != nil {
			util.HandleCmdErr(cmd, err)
		}
	}
}
