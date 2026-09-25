package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	goconfig "github.com/tarantool/go-config/v2"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/integrity"
)

var (
	errEndpointUnexpectedType       = errors.New("endpoint[")
	errEndpointUnknownTransportType = errors.New("endpoint[")
	errEtcdEndpointIsNotAString     = errors.New("etcd endpoint is not a string: ")
)

const defaultEtcdTimeout = 3 * time.Second

// cfgGetString is a small helper that reads a string value at path from cfg.
// Returns "" and no error when the path is not found.
func cfgGetString(cfg goconfig.Config, path string) (string, error) {
	var value string

	_, err := cfg.Get(goconfig.NewKeyPath(path), &value)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return "", nil
		}

		return "", fmt.Errorf("get %q: %w", path, err)
	}

	return value, nil
}

// cfgGetFloat64 is a small helper that reads a float64 value at path from cfg.
// Returns 0 and no error when the path is not found.
func cfgGetFloat64(cfg goconfig.Config, path string) (float64, error) {
	var value float64

	_, err := cfg.Get(goconfig.NewKeyPath(path), &value)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return 0, nil
		}

		return 0, fmt.Errorf("get %q: %w", path, err)
	}

	return value, nil
}

// cfgGetBool is a small helper that reads a bool value at path from cfg.
// Returns false and no error when the path is not found.
func cfgGetBool(cfg goconfig.Config, path string) (bool, error) {
	var value bool

	_, err := cfg.Get(goconfig.NewKeyPath(path), &value)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return false, nil
		}

		return false, fmt.Errorf("get %q: %w", path, err)
	}

	return value, nil
}

// NewCollectorFactory creates a cluster Factory configured for collecting
// data — integrity-aware (with verifiers) when integ has integrity configured.
// Returns a plain factory and nil error when integrity is not configured.
func NewCollectorFactory(integ integrity.IntegrityCtx) (sdkcluster.Factory, error) {
	hashers, verifiers, err := integrity.GetStorageVerifiers(integ)
	if errors.Is(err, integrity.ErrNotConfigured) {
		return sdkcluster.NewFactory(), nil
	}

	if err != nil {
		return sdkcluster.Factory{},
			fmt.Errorf("failed to create collectors with integrity check: %w", err)
	}

	return sdkcluster.NewFactory(
		sdkcluster.WithFileReadFunc(func(path string) (io.ReadCloser, error) {
			return integ.Repository.Read(path)
		}),
		sdkcluster.WithIntegrity(sdkcluster.IntegrityOptions{
			Hashers:         hashers,
			Signers:         nil,
			Verifiers:       verifiers,
			SignerVerifiers: nil,
		}),
	), nil
}

// NewPublisherFactory creates a cluster Factory configured for publishing
// data — integrity-aware (with signer/verifiers) when privateKey is set.
func NewPublisherFactory(privateKey string) (sdkcluster.Factory, error) {
	if privateKey == "" {
		return sdkcluster.NewFactory(), nil
	}

	hashers, signerVerifiers, err := integrity.GetStorageSigners(privateKey)
	if err != nil {
		return sdkcluster.Factory{},
			fmt.Errorf("failed to create publishers with integrity: %w", err)
	}

	return sdkcluster.NewFactory(
		sdkcluster.WithIntegrity(sdkcluster.IntegrityOptions{
			Hashers:         hashers,
			Signers:         nil,
			Verifiers:       nil,
			SignerVerifiers: signerVerifiers,
		}),
	), nil
}

// NewCollectorAndPublisherFactories returns separate collector- and
// publisher-oriented factories. They differ only in integrity options:
// collectors carry verifiers, publishers carry signer/verifiers.
func NewCollectorAndPublisherFactories(
	integ integrity.IntegrityCtx, privateKey string,
) (sdkcluster.Factory, sdkcluster.Factory, error) {
	collectors, err := NewCollectorFactory(integ)
	if err != nil {
		return sdkcluster.Factory{}, sdkcluster.Factory{}, fmt.Errorf("collector factory: %w", err)
	}

	publishers, err := NewPublisherFactory(privateKey)
	if err != nil {
		return sdkcluster.Factory{}, sdkcluster.Factory{}, fmt.Errorf("publisher factory: %w", err)
	}

	return collectors, publishers, nil
}

// CollectDataBytes collects raw []Data from a DataCollector, merges the YAML
// documents with first-wins priority (mirrors the former YamlDataMergeCollector
// behaviour), and returns the merged YAML bytes.
func CollectDataBytes(ctx context.Context, collector sdkcluster.DataCollector) ([]byte, error) {
	data, err := collector.Collect()
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, nil
	}

	mut, err := BuildMutableFromBytes(ctx, data[0].Value)
	if err != nil {
		return nil, fmt.Errorf("collect data: parse %q: %w", data[0].Source, err)
	}

	for _, item := range data[1:] {
		extra, err := BuildGoConfigFromBytes(ctx, item.Value)
		if err != nil {
			return nil, fmt.Errorf("collect data: parse %q: %w", item.Source, err)
		}

		err = fillOnlyMerge(ctx, mut, extra)
		if err != nil {
			return nil, fmt.Errorf("collect data: merge %q: %w", item.Source, err)
		}
	}

	snap := mut.Snapshot()

	merged, err := snap.MarshalYAML()
	if err != nil {
		return nil, fmt.Errorf("collect data: marshal: %w", err)
	}

	return merged, nil
}

// readStorageFromConfig extracts etcd or TCS endpoints from cfg, dials, reads
// the centralized config bytes, and parses them into a goconfig.Config.
//
// Returns (zero Config{}, nil, nil) if neither etcd nor storage endpoints are
// configured. Returns a cleanup func when an etcd client was opened.
func readStorageFromConfig(
	ctx context.Context,
	cfg goconfig.Config,
	integ integrity.IntegrityCtx,
) (goconfig.Config, func(), error) {
	collectorFactory, err := NewCollectorFactory(integ)
	if err != nil {
		return goconfig.Config{}, nil, fmt.Errorf("collector factory: %w", err)
	}

	// Try etcd first.
	etcdResult, cleanup, err := readEtcdEndpoints(ctx, cfg, collectorFactory)
	if err != nil {
		return goconfig.Config{}, nil, err
	}

	if etcdResult != nil {
		return *etcdResult, cleanup, nil
	}

	// Try TCS (Tarantool Config Storage).
	tcsResult, tcsFound, err := readTcsEndpoints(ctx, cfg, collectorFactory)
	if err != nil {
		return goconfig.Config{}, nil, err
	}

	if tcsFound {
		return tcsResult, nil, nil
	}

	return goconfig.Config{}, nil, nil
}

// readEtcdEndpoints reads etcd configuration from cfg, connects, and returns
// the parsed goconfig.Config plus a cleanup function.
// Returns (nil, nil, nil) if no etcd endpoints are configured.
func readEtcdEndpoints(
	ctx context.Context,
	cfg goconfig.Config,
	collectorFactory sdkcluster.Factory,
) (*goconfig.Config, func(), error) {
	// Read endpoints list.
	var rawEndpoints any

	_, err := cfg.Get(goconfig.NewKeyPath("config/etcd/endpoints"), &rawEndpoints)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return nil, nil, nil
		}

		return nil, nil, fmt.Errorf("read etcd endpoints: %w", err)
	}

	// Convert to []string.
	var endpoints []string

	switch typed := rawEndpoints.(type) {
	case []any:
		for _, e := range typed {
			s, ok := e.(string)
			if !ok {
				return nil, nil, fmt.Errorf("%w%T", errEtcdEndpointIsNotAString, e)
			}

			endpoints = append(endpoints, s)
		}
	case []string:
		endpoints = typed
	}

	if len(endpoints) == 0 {
		return nil, nil, nil
	}

	// Build EtcdOpts from cfg.
	username, err := cfgGetString(cfg, "config/etcd/username")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd username: %w", err)
	}

	password, err := cfgGetString(cfg, "config/etcd/password")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd password: %w", err)
	}

	keyFile, err := cfgGetString(cfg, "config/etcd/ssl/ssl_key")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd ssl key: %w", err)
	}

	certFile, err := cfgGetString(cfg, "config/etcd/ssl/ssl_cert")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd ssl cert: %w", err)
	}

	caPath, err := cfgGetString(cfg, "config/etcd/ssl/ca_path")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd ca path: %w", err)
	}

	caFile, err := cfgGetString(cfg, "config/etcd/ssl/ca_file")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd ca file: %w", err)
	}

	verifyPeer, err := cfgGetBool(cfg, "config/etcd/ssl/verify_peer")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd verify_peer: %w", err)
	}

	verifyHost, err := cfgGetBool(cfg, "config/etcd/ssl/verify_host")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd verify_host: %w", err)
	}

	timeoutSec, err := cfgGetFloat64(cfg, "config/etcd/http/request/timeout")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd timeout: %w", err)
	}

	prefix, err := cfgGetString(cfg, "config/etcd/prefix")
	if err != nil {
		return nil, nil, fmt.Errorf("read etcd prefix: %w", err)
	}

	timeout := defaultEtcdTimeout

	if timeoutSec != 0 {
		timeoutStr := fmt.Sprintf("%fs", timeoutSec)

		timeout, err = time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to parse etcd request timeout: %w", err)
		}
	}

	stor, gsCleanup, err := gsconnect.NewEtcdStorage(ctx, gsconnect.Config{
		Endpoints:   endpoints,
		Username:    username,
		Password:    password,
		DialTimeout: timeout,
		SSL: gsconnect.SSLConfig{
			KeyFile:    keyFile,
			CertFile:   certFile,
			CaPath:     caPath,
			CaFile:     caFile,
			VerifyPeer: verifyPeer,
			VerifyHost: verifyHost,
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("unable to connect to etcd: %w", err)
	}

	cleanup := func() { gsCleanup() }

	etcdCollector, err := collectorFactory.NewRemoteStorage(stor, prefix, "", timeout, "etcd")
	if err != nil {
		cleanup()

		return nil, nil, fmt.Errorf("failed to create etcd collector: %w", err)
	}

	rawBytes, err := CollectDataBytes(ctx, etcdCollector)
	if err != nil {
		cleanup()

		return nil, nil, fmt.Errorf("unable to get config from etcd: %w", err)
	}

	parsedCfg, err := BuildGoConfigFromBytes(ctx, rawBytes)
	if err != nil {
		cleanup()

		return nil, nil, fmt.Errorf("unable to parse etcd config: %w", err)
	}

	return &parsedCfg, cleanup, nil
}

// readTcsEndpoints reads TCS (Tarantool Config Storage) configuration from cfg,
// connects to the first reachable endpoint, and returns the parsed goconfig.Config.
// The boolean result is false if no TCS endpoints are configured.
//
// Per TCS semantics: stop on first successful connect, aggregate errors only if
// all endpoints fail.
func readTcsEndpoints(
	ctx context.Context,
	cfg goconfig.Config,
	collectorFactory sdkcluster.Factory,
) (goconfig.Config, bool, error) {
	// Read endpoints list as []any (each element is a map[string]any).
	var rawEndpoints any

	_, err := cfg.Get(goconfig.NewKeyPath("config/storage/endpoints"), &rawEndpoints)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return goconfig.Config{}, false, nil
		}

		return goconfig.Config{}, false, fmt.Errorf("read storage endpoints: %w", err)
	}

	endpointList, ok := rawEndpoints.([]any)
	if !ok || len(endpointList) == 0 {
		return goconfig.Config{}, false, nil
	}

	prefix, err := cfgGetString(cfg, "config/storage/prefix")
	if err != nil {
		return goconfig.Config{}, false, fmt.Errorf("read storage prefix: %w", err)
	}

	timeoutSec, err := cfgGetFloat64(cfg, "config/storage/timeout")
	if err != nil {
		return goconfig.Config{}, false, fmt.Errorf("read storage timeout: %w", err)
	}

	timeout := time.Duration(timeoutSec * float64(time.Second))

	var connectionErrors []error

	for idx, rawEp := range endpointList {
		epMap, ok := rawEp.(map[string]any)
		if !ok {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("%w%d]: unexpected type %T", errEndpointUnexpectedType, idx, rawEp))

			continue
		}

		uri, _ := epMap["uri"].(string)
		login, _ := epMap["login"].(string)
		password, _ := epMap["password"].(string)

		var params map[string]any

		if p, ok := epMap["params"]; ok {
			params, _ = p.(map[string]any)
		}

		sslKeyFile, _ := params["ssl_key_file"].(string)
		sslCertFile, _ := params["ssl_cert_file"].(string)
		sslCaFile, _ := params["ssl_ca_file"].(string)
		sslCiphers, _ := params["ssl_ciphers"].(string)
		sslPassword, _ := params["ssl_password"].(string)
		sslPasswordFile, _ := params["ssl_password_file"].(string)
		transport, _ := params["transport"].(string)

		var network, address string

		if !connect.IsBaseURI(uri) {
			network = "tcp"
			address = uri
		} else {
			network, address = connect.ParseBaseURI(uri)
		}

		addr := fmt.Sprintf("%s://%s", network, address)

		var sslEnable bool

		switch transport {
		case "ssl":
			sslEnable = true
		case "plain":
			sslEnable = false
		case "":
			sslEnable = sslKeyFile != "" || sslCertFile != "" || sslCaFile != "" ||
				sslCiphers != "" || sslPassword != "" || sslPasswordFile != ""
		default:
			connectionErrors = append(connectionErrors, fmt.Errorf(
				"%w%d] %q: unknown transport type: %s",
				errEndpointUnknownTransportType, idx, addr, transport))

			continue
		}

		stor, gsCleanup, err := gsconnect.NewTCSStorage(ctx, gsconnect.Config{
			Endpoints:   []string{addr},
			Username:    login,
			Password:    password,
			DialTimeout: timeout,
			SSL: gsconnect.SSLConfig{
				Enable:       sslEnable,
				KeyFile:      sslKeyFile,
				CertFile:     sslCertFile,
				CaFile:       sslCaFile,
				Ciphers:      sslCiphers,
				Password:     sslPassword,
				PasswordFile: sslPasswordFile,
				VerifyPeer:   sslEnable,
				VerifyHost:   sslEnable,
			},
		})
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("endpoint[%d] %q: connect: %w", idx, addr, err))

			continue
		}
		defer gsCleanup()

		tcsCollector, err := collectorFactory.NewRemoteStorage(
			stor,
			prefix,
			"",
			timeout,
			"tarantool",
		)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("endpoint[%d] %q: create collector: %w", idx, addr, err))

			continue
		}

		rawBytes, err := CollectDataBytes(ctx, tcsCollector)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("endpoint[%d] %q: collect: %w", idx, addr, err))

			continue
		}

		parsedCfg, err := BuildGoConfigFromBytes(ctx, rawBytes)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("endpoint[%d] %q: parse config: %w", idx, addr, err))

			continue
		}

		// First reachable endpoint wins.
		return parsedCfg, true, nil
	}

	if len(connectionErrors) > 0 {
		return goconfig.Config{}, false, errors.Join(connectionErrors...)
	}

	return goconfig.Config{}, false, nil
}
