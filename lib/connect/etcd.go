package connect

import (
	"fmt"
	"os"

	libcluster "github.com/tarantool/tt/lib/cluster"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdFunc is a function that can be called on an `etcd` connection.
type EtcdFunc func(*clientv3.Client) error

// makeEtcdOptsFromUriOpts create etcd connect options from URI options.
func makeEtcdOptsFromUriOpts(src UriOpts) libcluster.EtcdOpts {
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

// connectEtcd establishes a connection to etcd.
func connectEtcd(uriOpts UriOpts) (*clientv3.Client, error) {
	etcdOpts := makeEtcdOptsFromUriOpts(uriOpts)
	if etcdOpts.Username == "" && etcdOpts.Password == "" {
		if etcdOpts.Username == "" {
			etcdOpts.Username = os.Getenv(EtcdUsernameEnv)
		}
		if etcdOpts.Password == "" {
			etcdOpts.Password = os.Getenv(EtcdPasswordEnv)
		}
	}

	c, err := libcluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	return c, nil
}

// RunOnEtcd runs the provided function with etcd connection.
// Returns true if the function was executed.
func RunOnEtcd(opts UriOpts, f EtcdFunc) (bool, error) {
	if f != nil {
		c, err := connectEtcd(opts)
		if err != nil {
			return false, err
		}
		return true, f(c)
	}
	return false, nil
}
