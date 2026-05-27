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

// createCollectorFactory creates a cluster Factory configured for collecting
// data — integrity-aware (with verifiers) when the integrity context is set.
func createCollectorFactory(ctx integrity.IntegrityCtx) (libcluster.Factory, error) {
	hashers, verifiers, err := integrity.GetStorageVerifiers(ctx)
	if errors.Is(err, integrity.ErrNotConfigured) {
		return libcluster.NewFactory(), nil
	}
	if err != nil {
		return libcluster.Factory{},
			fmt.Errorf("failed to create collectors with integrity check: %w", err)
	}
	return libcluster.NewFactory(
		libcluster.WithFileReadFunc(func(path string) (io.ReadCloser, error) {
			return cmdCtx.Integrity.Repository.Read(path)
		}),
		libcluster.WithIntegrity(libcluster.IntegrityOptions{
			Hashers:   hashers,
			Verifiers: verifiers,
		}),
	), nil
}

// createPublisherFactory creates a cluster Factory configured for publishing
// data — integrity-aware (with signer/verifiers) when a private key is set.
func createPublisherFactory(privateKey string) (libcluster.Factory, error) {
	if privateKey == "" {
		return libcluster.NewFactory(), nil
	}
	hashers, signerVerifiers, err := integrity.GetStorageSigners(privateKey)
	if err != nil {
		return libcluster.Factory{},
			fmt.Errorf("failed to create publishers with integrity: %w", err)
	}
	return libcluster.NewFactory(
		libcluster.WithIntegrity(libcluster.IntegrityOptions{
			Hashers:         hashers,
			SignerVerifiers: signerVerifiers,
		}),
	), nil
}

// createCollectorAndPublisherFactories returns separate collector- and
// publisher-oriented factories. They differ only in integrity options:
// collectors carry verifiers, publishers carry signer/verifiers.
func createCollectorAndPublisherFactories(
	ctx integrity.IntegrityCtx, privateKey string,
) (libcluster.Factory, libcluster.Factory, error) {
	collectors, err := createCollectorFactory(ctx)
	if err != nil {
		return libcluster.Factory{}, libcluster.Factory{}, err
	}
	publishers, err := createPublisherFactory(privateKey)
	if err != nil {
		return libcluster.Factory{}, libcluster.Factory{}, err
	}
	return collectors, publishers, nil
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
