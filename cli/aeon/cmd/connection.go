package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"
	goconfig "github.com/tarantool/go-config/v2"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errInvalidConnectionURL      = errors.New("invalid connection url")
	errTransportMustBeSSLOrPlain = errors.New("transport must be ssl or plain")
)

// FillConnectCtx takes a ConnectCtx object and fills it with data from a
// collected configuration by given instanceName and sdkconnect.UriOpts.
// It returns an error if fails to collect a configuration,
// instantiate a cluster config or find an instance in the cluster.
func FillConnectCtx(connectCtx *ConnectCtx, uriOpts sdkconnect.URIOpts,
	instanceName string, factory sdkcluster.Factory,
) error {
	connOpts := sdkcluster.ConnectOpts{
		Username: connectCtx.Username,
		Password: connectCtx.Password,
	}
	stor, cleanup, storageType, err := sdkcluster.NewStorageConnection(connOpts, uriOpts)
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
	_, err = instCfg.Get(goconfig.NewKeyPath("roles_cfg/aeon.grpc/advertise"), &rawAdvertise)
	if err != nil {
		return fmt.Errorf("failed to get aeon advertise: %w", err)
	}

	var advertise Advertise
	if err = mapstructure.Decode(rawAdvertise, &advertise); err != nil {
		return fmt.Errorf("failed to decode aeon advertise: %w", err)
	}

	if advertise.URI == "" {
		return errInvalidConnectionURL
	}

	cleanedURL, err := util.RemoveScheme(advertise.URI)
	if err != nil {
		return err
	}

	connectCtx.Network, connectCtx.Address = sdkconnect.ParseBaseURI(cleanedURL)

	if (advertise.Params.Transport != "ssl") && (advertise.Params.Transport != "plain") {
		return errTransportMustBeSSLOrPlain
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
