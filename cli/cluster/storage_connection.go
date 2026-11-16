package cluster

import (
	"fmt"

	goconfig "github.com/tarantool/go-config/v2"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
)

// The kinds of a config storage (StorageConnection.Kind).
const (
	// StorageKindEtcd is etcd, config.etcd of a cluster config.
	StorageKindEtcd = storageEtcd
	// StorageKindTarantool is a Tarantool config storage, config.storage of
	// a cluster config.
	StorageKindTarantool = storageTarantool
)

// StorageConnection is how to reach the config storage the file part of a
// cluster config points to, config.etcd or config.storage, as tarantool
// reaches it: for a program other than tt to be pointed at the same storage,
// such as TCM.
type StorageConnection struct {
	// Kind is StorageKindEtcd or StorageKindTarantool.
	Kind string
	// Prefix is the prefix of the keys of the cluster config.
	Prefix string
	// Endpoints are the endpoints as the config writes them: the URLs of
	// etcd, the URIs of the instances of a Tarantool config storage.
	Endpoints []string
	// Username and Password are the credentials of the storage: those of
	// config.etcd, or those of the first endpoint of config.storage.
	Username string
	Password string
	// SSL is the TLS of the connection to etcd; zero for a Tarantool config
	// storage, whose TLS goes with each of its endpoints.
	SSL gsconnect.SSLConfig
}

// ReadStorageConnection returns the config storage cfg, the file part of a
// cluster config, points to; found is false when it points to none, the
// cluster config being the file itself.
func ReadStorageConnection(cfg goconfig.Config) (StorageConnection, bool, error) {
	ref, found, err := readEtcdRef(cfg)
	if err != nil {
		return StorageConnection{}, false, err
	}

	if found {
		endpoint := ref.endpoints[0].config

		return StorageConnection{
			Kind:      StorageKindEtcd,
			Prefix:    ref.prefix,
			Endpoints: endpoint.Endpoints,
			Username:  endpoint.Username,
			Password:  endpoint.Password,
			SSL:       endpoint.SSL,
		}, true, nil
	}

	return readTcsConnection(cfg)
}

// readTcsConnection returns the Tarantool config storage config.storage of
// cfg points to, with the URIs of its endpoints as they are written.
func readTcsConnection(cfg goconfig.Config) (StorageConnection, bool, error) {
	var none StorageConnection

	var raw any

	_, found := cfg.Lookup(goconfig.NewKeyPath("config/storage/endpoints"))
	if !found {
		return none, false, nil
	}

	_, err := cfg.Get(goconfig.NewKeyPath("config/storage/endpoints"), &raw)
	if err != nil {
		return none, false, fmt.Errorf("read storage endpoints: %w", err)
	}

	list, _ := raw.([]any)

	conn := StorageConnection{
		Kind: StorageKindTarantool, Prefix: "", Endpoints: nil, Username: "", Password: "",
		SSL: gsconnect.SSLConfig{},
	}

	for _, item := range list {
		fields, _ := item.(map[string]any)

		uri, _ := fields["uri"].(string)
		if uri == "" {
			continue
		}

		if len(conn.Endpoints) == 0 {
			conn.Username, _ = fields["login"].(string)
			conn.Password, _ = fields["password"].(string)
		}

		conn.Endpoints = append(conn.Endpoints, uri)
	}

	if len(conn.Endpoints) == 0 {
		return none, false, nil
	}

	conn.Prefix, err = cfgGetString(cfg, "config/storage/prefix")
	if err != nil {
		return none, false, fmt.Errorf("read storage prefix: %w", err)
	}

	return conn, true, nil
}
