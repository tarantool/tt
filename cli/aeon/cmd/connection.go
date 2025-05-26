package cmd

import (
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/util"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

// FillConnectCtx takes a ConnectCtx object and fills it with data from a
// collected configuration by given instanceName and libconnect.UriOpts.
// It returns an error if fails to collect a configuration,
// instantiate a cluster config or find an instance in the cluster.
func FillConnectCtx(connectCtx *ConnectCtx, uriOpts libconnect.UriOpts,
	instanceName string, collectors libcluster.CollectorFactory,
) error {
	connOpts := libcluster.ConnectOpts{
		Username: connectCtx.Username,
		Password: connectCtx.Password,
	}
	collector, cancel, err := libcluster.CreateCollector(collectors,
		connOpts, uriOpts)
	if err != nil {
		return err
	}
	defer cancel()

	config, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration: %w", err)
	}

	clusterConfig, err := libcluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	result := libcluster.Instantiate(clusterConfig, instanceName)

	dataSsl := []string{"roles_cfg", "aeon.grpc", "advertise"}
	data, err := result.Get(dataSsl)
	if err != nil {
		return err
	}

	var advertise Advertise
	err = mapstructure.Decode(data, &advertise)
	if err != nil {
		return err
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
