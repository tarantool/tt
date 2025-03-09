package cmd

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/util"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

func ShowUri(connectCtx *ConnectCtx, uri *url.URL,
	instanceName string, collectors libcluster.CollectorFactory) error {
	uriOpts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	connOpts := connectOpts{
		Username: connectCtx.Username,
		Password: connectCtx.Password,
	}
	_, collector, cancel, err := createPublisherAndCollector(
		nil,
		collectors,
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
