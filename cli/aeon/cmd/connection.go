package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"
	goconfig "github.com/tarantool/go-config"
	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/util"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

// FillConnectCtx takes a ConnectCtx object and fills it with data from a
// collected configuration by given instanceName and libconnect.UriOpts.
// It returns an error if fails to collect a configuration,
// instantiate a cluster config or find an instance in the cluster.
func FillConnectCtx(connectCtx *ConnectCtx, uriOpts libconnect.UriOpts,
	instanceName string, factory libcluster.Factory,
) error {
	connOpts := libcluster.ConnectOpts{
		Username: connectCtx.Username,
		Password: connectCtx.Password,
	}
	stor, cleanup, storageType, err := libcluster.NewStorageConnection(connOpts, uriOpts)
	if err != nil {
		return err
	}
	defer cleanup()

	collector, err := factory.NewRemoteStorage(stor, uriOpts.Prefix,
		uriOpts.Params["key"], uriOpts.Timeout, storageType)
	if err != nil {
		return fmt.Errorf("failed to create %s collector: %w", storageType, err)
	}

	rawBytes, err := cluster.CollectDataBytes(context.Background(), collector)
	if err != nil {
		return fmt.Errorf("failed to collect a configuration: %w", err)
	}

	goView, err := cluster.BuildGoConfigFromBytes(context.Background(), rawBytes)
	if err != nil {
		return fmt.Errorf("failed to parse cluster config: %w", err)
	}

	instCfg, err := cluster.InstanceConfig(goView, instanceName)
	if err != nil {
		return fmt.Errorf("instance %q not found: %w", instanceName, err)
	}

	var rawAdvertise any
	if _, err = instCfg.Get(goconfig.NewKeyPath("roles_cfg/aeon.grpc/advertise"), &rawAdvertise); err != nil {
		return fmt.Errorf("failed to get aeon advertise: %w", err)
	}

	var advertise Advertise
	if err = mapstructure.Decode(rawAdvertise, &advertise); err != nil {
		return fmt.Errorf("failed to decode aeon advertise: %w", err)
	}

	if advertise.Uri == "" {
		return errors.New("invalid connection url")
	}

	cleanedURL, err := util.RemoveScheme(advertise.Uri)
	if err != nil {
		return err
	}

	connectCtx.Network, connectCtx.Address = libconnect.ParseBaseURI(cleanedURL)

	if (advertise.Params.Transport != "ssl") && (advertise.Params.Transport != "plain") {
		return errors.New("transport must be ssl or plain")
	}

	if advertise.Params.Transport == "ssl" {
		connectCtx.Transport = TransportSsl

		connectCtx.Ssl = Ssl{
			KeyFile:  advertise.Params.KeyFile,
			CertFile: advertise.Params.CertFile,
			CaFile:   advertise.Params.CaFile,
		}
	}

	return nil
}
