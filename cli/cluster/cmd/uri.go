package cmd

import (
	"fmt"

	"github.com/tarantool/go-tarantool/v2"

	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/dial"
)

// MakeEtcdOptsFromUriOpts create etcd connect options from URI options.
func MakeEtcdOptsFromUriOpts(src connect.UriOpts) libcluster.EtcdOpts {
	var endpoints []string
	if src.Endpoint != "" {
		endpoints = []string{src.Endpoint}
	}

	return libcluster.EtcdOpts{
		Endpoints:      endpoints,
		Username:       src.Username,
		Password:       src.Password,
		KeyFile:        src.KeyFile,
		CertFile:       src.CertFile,
		CaPath:         src.CaPath,
		CaFile:         src.CaFile,
		SkipHostVerify: src.SkipHostVerify || src.SkipPeerVerify,
		Timeout:        src.Timeout,
	}
}

// MakeConnectOptsFromUriOpts create Tarantool connect options from
// URI options.
func MakeConnectOptsFromUriOpts(src connect.UriOpts) (tarantool.Dialer, tarantool.Opts, error) {
	address := fmt.Sprintf("tcp://%s", src.Host)

	dialer, err := dial.New(dial.Opts{
		Address:     address,
		User:        src.Username,
		Password:    src.Password,
		SslKeyFile:  src.KeyFile,
		SslCertFile: src.CertFile,
		SslCaFile:   src.CaFile,
		SslCiphers:  src.Ciphers,
	})
	if err != nil {
		return nil, tarantool.Opts{}, err
	}

	opts := tarantool.Opts{
		Timeout: src.Timeout,
	}

	return dialer, opts, nil
}
