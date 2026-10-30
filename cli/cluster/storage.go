package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	goconfig "github.com/tarantool/go-config/v2"
	gcttarantool "github.com/tarantool/go-config/v2/tarantool"
	"github.com/tarantool/go-storage/v2"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

var (
	errEndpointUnexpectedType       = errors.New("endpoint[")
	errEndpointUnknownTransportType = errors.New("endpoint[")
	errEtcdEndpointIsNotAString     = errors.New("etcd endpoint is not a string: ")
	errNoStorageEndpoints           = errors.New("names no endpoints of the config storage")
)

// ErrConfigFile is wrapped by the error of OpenConfigStorage for a cluster
// config file that cannot be read as one.
var ErrConfigFile = errors.New("cannot read the cluster config file")

const defaultEtcdTimeout = 3 * time.Second

// storageKeys are the sections of a cluster config that point to the config
// storage the rest of the cluster config is kept in.
var storageKeys = []string{"config.etcd", "config.storage"}

// StorageReference returns the section of cfg, the file part of a cluster
// config, that points to a config storage, config.etcd or config.storage,
// and false when it has neither: the cluster config is then the file
// itself. A section without endpoints counts as well, since tarantool takes
// it for one.
func StorageReference(cfg goconfig.Config) (string, bool) {
	for _, key := range storageKeys {
		_, found := cfg.Lookup(goconfig.NewKeyPath(strings.ReplaceAll(key, ".", "/")))
		if found {
			return key, true
		}
	}

	return "", false
}

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
// Returns defaultValue and no error when the path is not found.
func cfgGetBool(cfg goconfig.Config, path string, defaultValue bool) (bool, error) {
	value := defaultValue

	_, err := cfg.Get(goconfig.NewKeyPath(path), &value)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return defaultValue, nil
		}

		return false, fmt.Errorf("get %q: %w", path, err)
	}

	return value, nil
}

// NewCollectorFactory creates a cluster Factory configured for collecting
// data — integrity-aware (with verifiers) when integ has integrity configured.
// Returns a plain factory and nil error when integrity is not configured.
func NewCollectorFactory(integ integrity.IntegrityCtx) (sdkcluster.Factory, error) {
	hashers, verifiers, err := integrityprovider.Get().GetStorageVerifiers(integ)
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

	hashers, signerVerifiers, err := integrityprovider.Get().GetStorageSigners(privateKey)
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

// The types of a config storage, as sdkcluster names them.
const (
	storageEtcd      = "etcd"
	storageTarantool = "tarantool"
)

// storageRef is the config storage the file part of a cluster config points
// to with config.etcd or config.storage: where tarantool takes the rest of
// its cluster config from, and where tt reads it and publishes into it.
type storageRef struct {
	// kind is the type of the storage: storageEtcd or storageTarantool.
	kind string
	// prefix is the prefix of the keys of the cluster config.
	prefix string
	// timeout bounds a request to the storage.
	timeout time.Duration
	// endpoints are the connections to try, in order: one to every endpoint
	// of etcd, and one per endpoint of a Tarantool config storage, of which
	// the first that answers serves.
	endpoints []storageEndpoint
	// errs are the endpoints of a Tarantool config storage that cannot be
	// tried, and why.
	errs []error
}

// storageEndpoint is one connection of a storageRef.
type storageEndpoint struct {
	// name names the endpoint of a Tarantool config storage for a message;
	// empty for etcd, whose messages name the storage.
	name string
	// config is what to connect with.
	config gsconnect.Config
}

// open connects to endpoint, one of the storage. The caller says what
// failed.
//
//nolint:wrapcheck
func (ref storageRef) open(ctx context.Context, endpoint storageEndpoint) (
	storage.Storage, gsconnect.CleanupFunc, error,
) {
	if ref.kind == storageEtcd {
		return gsconnect.NewEtcdStorage(ctx, endpoint.config)
	}

	return sdkcluster.NewTCSStorage(ctx, endpoint.config)
}

// readStorageRef returns the config storage cfg points to: etcd when
// config.etcd has endpoints, and a Tarantool config storage when
// config.storage has them. found is false when neither has.
func readStorageRef(cfg goconfig.Config) (storageRef, bool, error) {
	ref, found, err := readEtcdRef(cfg)
	if err != nil || found {
		return ref, found, err
	}

	return readTcsRef(cfg)
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

	ref, found, err := readStorageRef(cfg)
	if err != nil || !found {
		return goconfig.Config{}, nil, err
	}

	if ref.kind == storageEtcd {
		return readEtcd(ctx, ref, collectorFactory)
	}

	tcsResult, err := readTcs(ctx, ref, collectorFactory)
	if err != nil {
		return goconfig.Config{}, nil, err
	}

	return tcsResult, nil, nil
}

// readEtcdRef reads the etcd config.etcd of cfg points to. found is false
// when it has no endpoints.
func readEtcdRef(cfg goconfig.Config) (storageRef, bool, error) {
	var none storageRef

	// Read endpoints list.
	var rawEndpoints any

	_, err := cfg.Get(goconfig.NewKeyPath("config/etcd/endpoints"), &rawEndpoints)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return none, false, nil
		}

		return none, false, fmt.Errorf("read etcd endpoints: %w", err)
	}

	// Convert to []string.
	var endpoints []string

	switch typed := rawEndpoints.(type) {
	case []any:
		for _, e := range typed {
			s, ok := e.(string)
			if !ok {
				return none, false, fmt.Errorf("%w%T", errEtcdEndpointIsNotAString, e)
			}

			endpoints = append(endpoints, s)
		}
	case []string:
		endpoints = typed
	}

	if len(endpoints) == 0 {
		return none, false, nil
	}

	// Build EtcdOpts from cfg.
	username, err := cfgGetString(cfg, "config/etcd/username")
	if err != nil {
		return none, false, fmt.Errorf("read etcd username: %w", err)
	}

	password, err := cfgGetString(cfg, "config/etcd/password")
	if err != nil {
		return none, false, fmt.Errorf("read etcd password: %w", err)
	}

	keyFile, err := cfgGetString(cfg, "config/etcd/ssl/ssl_key")
	if err != nil {
		return none, false, fmt.Errorf("read etcd ssl key: %w", err)
	}

	certFile, err := cfgGetString(cfg, "config/etcd/ssl/ssl_cert")
	if err != nil {
		return none, false, fmt.Errorf("read etcd ssl cert: %w", err)
	}

	caPath, err := cfgGetString(cfg, "config/etcd/ssl/ca_path")
	if err != nil {
		return none, false, fmt.Errorf("read etcd ca path: %w", err)
	}

	caFile, err := cfgGetString(cfg, "config/etcd/ssl/ca_file")
	if err != nil {
		return none, false, fmt.Errorf("read etcd ca file: %w", err)
	}

	sslEnable := slices.ContainsFunc(endpoints, func(endpoint string) bool {
		return strings.HasPrefix(endpoint, "https://")
	})

	verifyPeer, err := cfgGetBool(cfg, "config/etcd/ssl/verify_peer", sslEnable)
	if err != nil {
		return none, false, fmt.Errorf("read etcd verify_peer: %w", err)
	}

	verifyHost, err := cfgGetBool(cfg, "config/etcd/ssl/verify_host", sslEnable)
	if err != nil {
		return none, false, fmt.Errorf("read etcd verify_host: %w", err)
	}

	timeoutSec, err := cfgGetFloat64(cfg, "config/etcd/http/request/timeout")
	if err != nil {
		return none, false, fmt.Errorf("read etcd timeout: %w", err)
	}

	prefix, err := cfgGetString(cfg, "config/etcd/prefix")
	if err != nil {
		return none, false, fmt.Errorf("read etcd prefix: %w", err)
	}

	timeout := defaultEtcdTimeout

	if timeoutSec != 0 {
		timeoutStr := fmt.Sprintf("%fs", timeoutSec)

		timeout, err = time.ParseDuration(timeoutStr)
		if err != nil {
			return none, false, fmt.Errorf("unable to parse etcd request timeout: %w", err)
		}
	}

	// Match URI connections: HTTPS encrypts without server verification when
	// no custom CA is configured, and verifies against an explicitly supplied CA.
	if sslEnable && caFile == "" && caPath == "" {
		verifyPeer = false
		verifyHost = false
	}

	return storageRef{
		kind:    storageEtcd,
		prefix:  prefix,
		timeout: timeout,
		endpoints: []storageEndpoint{{
			name: "",
			config: gsconnect.Config{
				Endpoints:   endpoints,
				Username:    username,
				Password:    password,
				DialTimeout: timeout,
				SSL: gsconnect.SSLConfig{
					Enable:     sslEnable,
					KeyFile:    keyFile,
					CertFile:   certFile,
					CaPath:     caPath,
					CaFile:     caFile,
					VerifyPeer: verifyPeer,
					VerifyHost: verifyHost,
				},
			},
		}},
		errs: nil,
	}, true, nil
}

// readEtcd connects to the etcd ref names, and returns the parsed
// goconfig.Config plus a cleanup function.
func readEtcd(
	ctx context.Context,
	ref storageRef,
	collectorFactory sdkcluster.Factory,
) (goconfig.Config, func(), error) {
	stor, gsCleanup, err := ref.open(ctx, ref.endpoints[0])
	if err != nil {
		return goconfig.Config{}, nil, fmt.Errorf("unable to connect to etcd: %w", err)
	}

	cleanup := func() { gsCleanup() }

	etcdCollector, err := collectorFactory.NewRemoteStorage(stor, ref.prefix, "", ref.timeout,
		storageEtcd)
	if err != nil {
		cleanup()

		return goconfig.Config{}, nil, fmt.Errorf("failed to create etcd collector: %w", err)
	}

	rawBytes, err := CollectDataBytes(ctx, etcdCollector)
	if err != nil {
		cleanup()

		return goconfig.Config{}, nil, fmt.Errorf("unable to get config from etcd: %w", err)
	}

	parsedCfg, err := BuildGoConfigFromBytes(ctx, rawBytes)
	if err != nil {
		cleanup()

		return goconfig.Config{}, nil, fmt.Errorf("unable to parse etcd config: %w", err)
	}

	return parsedCfg, cleanup, nil
}

// readTcsRef reads the TCS (Tarantool Config Storage) config.storage of cfg
// points to. found is false when it has no endpoints. An endpoint that
// cannot be tried goes to the errs of the result.
func readTcsRef(cfg goconfig.Config) (storageRef, bool, error) {
	var none storageRef

	// Read endpoints list as []any (each element is a map[string]any).
	var rawEndpoints any

	_, err := cfg.Get(goconfig.NewKeyPath("config/storage/endpoints"), &rawEndpoints)
	if err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			return none, false, nil
		}

		return none, false, fmt.Errorf("read storage endpoints: %w", err)
	}

	endpointList, ok := rawEndpoints.([]any)
	if !ok || len(endpointList) == 0 {
		return none, false, nil
	}

	prefix, err := cfgGetString(cfg, "config/storage/prefix")
	if err != nil {
		return none, false, fmt.Errorf("read storage prefix: %w", err)
	}

	timeoutSec, err := cfgGetFloat64(cfg, "config/storage/timeout")
	if err != nil {
		return none, false, fmt.Errorf("read storage timeout: %w", err)
	}

	ref := storageRef{
		kind:      storageTarantool,
		prefix:    prefix,
		timeout:   time.Duration(timeoutSec * float64(time.Second)),
		endpoints: nil,
		errs:      nil,
	}

	for idx, rawEp := range endpointList {
		endpoint, err := tcsEndpoint(idx, rawEp, ref.timeout)
		if err != nil {
			ref.errs = append(ref.errs, err)

			continue
		}

		ref.endpoints = append(ref.endpoints, endpoint)
	}

	return ref, true, nil
}

// tcsEndpoint returns the connection to the endpoint idx of config.storage,
// rawEp.
func tcsEndpoint(idx int, rawEp any, timeout time.Duration) (storageEndpoint, error) {
	epMap, ok := rawEp.(map[string]any)
	if !ok {
		return storageEndpoint{}, fmt.Errorf("%w%d]: unexpected type %T",
			errEndpointUnexpectedType, idx, rawEp)
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

	switch {
	case strings.HasPrefix(uri, "http://"), strings.HasPrefix(uri, "https://"):
		uriOpts, err := connect.CreateURIOpts(uri)
		if err != nil {
			return storageEndpoint{}, fmt.Errorf("endpoint[%d] %q: invalid URI: %w", idx,
				log.RedactTarget(uri), err)
		}

		network, address = connect.TCPNetwork, uriOpts.Host
	case !connect.IsBaseURI(uri):
		network, address = "tcp", uri
	default:
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
		sslEnable = strings.HasPrefix(uri, "https://") || sslKeyFile != "" ||
			sslCertFile != "" || sslCaFile != "" ||
			sslCiphers != "" || sslPassword != "" || sslPasswordFile != ""
	default:
		return storageEndpoint{}, fmt.Errorf("%w%d] %q: unknown transport type: %s",
			errEndpointUnknownTransportType, idx, addr, transport)
	}

	return storageEndpoint{
		name: fmt.Sprintf("endpoint[%d] %q", idx, addr),
		config: gsconnect.Config{
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
		},
	}, nil
}

// readTcs connects to the first reachable endpoint of the TCS ref names, and
// returns the parsed goconfig.Config.
//
// Per TCS semantics: stop on first successful connect, aggregate errors only if
// all endpoints fail.
func readTcs(
	ctx context.Context,
	ref storageRef,
	collectorFactory sdkcluster.Factory,
) (goconfig.Config, error) {
	connectionErrors := slices.Clone(ref.errs)

	for _, endpoint := range ref.endpoints {
		stor, gsCleanup, err := ref.open(ctx, endpoint)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("%s: connect: %w", endpoint.name, err))

			continue
		}
		defer gsCleanup()

		tcsCollector, err := collectorFactory.NewRemoteStorage(
			stor,
			ref.prefix,
			"",
			ref.timeout,
			storageTarantool,
		)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("%s: create collector: %w", endpoint.name, err))

			continue
		}

		rawBytes, err := CollectDataBytes(ctx, tcsCollector)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("%s: collect: %w", endpoint.name, err))

			continue
		}

		parsedCfg, err := BuildGoConfigFromBytes(ctx, rawBytes)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("%s: parse config: %w", endpoint.name, err))

			continue
		}

		// First reachable endpoint wins.
		return parsedCfg, nil
	}

	if len(connectionErrors) > 0 {
		return goconfig.Config{}, errors.Join(connectionErrors...)
	}

	return goconfig.Config{}, nil
}

// ConfigStorage is the config storage a cluster config file points to,
// connected (OpenConfigStorage).
type ConfigStorage struct {
	// Collector reads the cluster config kept in the storage.
	Collector sdkcluster.DataCollector
	// Publisher writes it.
	Publisher sdkcluster.DataPublisher
	// FileInstances are the instances the file declares itself, next to the
	// reference to the storage: they are in the topology whatever the
	// storage holds, as the file wins over the storage.
	FileInstances []string

	cleanup gsconnect.CleanupFunc
}

// Close closes the connection to the storage.
func (s *ConfigStorage) Close() {
	s.cleanup()
}

// OpenConfigStorage connects to the config storage that the cluster config
// file at path points to with config.etcd or config.storage, as tarantool
// reads them from the file and the TT_ environment: its endpoints, prefix,
// credentials and TLS settings. It returns false when the file points to no
// storage. A Tarantool config storage is served by the first of its
// endpoints that can be connected to.
//
// The collector and the publisher come from collectors and publishers, as
// for a storage given by its URI. A file that names a storage without
// endpoints is an error: there is nowhere to publish to, and writing the
// file over would drop its reference to the storage.
func OpenConfigStorage(ctx context.Context, path string,
	collectors, publishers sdkcluster.Factory,
) (*ConfigStorage, bool, error) {
	mut, err := gcttarantool.New().
		WithoutValidation().
		WithEnvIgnore("TT_*_DEFAULT").
		WithConfigFile(path).
		BuildMutable(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("%w %q: %w", ErrConfigFile, path, err)
	}

	cfg := mut.Snapshot()

	key, referenced := StorageReference(cfg)
	if !referenced {
		return nil, false, nil
	}

	ref, found, err := readStorageRef(cfg)
	if err != nil {
		return nil, false, err
	}

	if !found {
		return nil, false, fmt.Errorf("%s of %s %w", key, path, errNoStorageEndpoints)
	}

	fileInstances, err := Instances(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the topology of %s: %w", path, err)
	}

	connectionErrors := slices.Clone(ref.errs)

	for _, endpoint := range ref.endpoints {
		stor, cleanup, err := ref.open(ctx, endpoint)
		if err != nil {
			connectionErrors = append(connectionErrors, fmt.Errorf("unable to connect to "+
				"the %s of %s: %w", key, path, err))

			continue
		}

		publisher, err := publishers.NewRemoteStorage(stor, ref.prefix, "", ref.timeout,
			ref.kind)
		if err != nil {
			cleanup()

			return nil, false, fmt.Errorf("failed to create %s publisher: %w", ref.kind, err)
		}

		collector, err := collectors.NewRemoteStorage(stor, ref.prefix, "", ref.timeout,
			ref.kind)
		if err != nil {
			cleanup()

			return nil, false, fmt.Errorf("failed to create %s collector: %w", ref.kind, err)
		}

		return &ConfigStorage{
			Collector:     collector,
			Publisher:     publisher,
			FileInstances: fileInstances,
			cleanup:       cleanup,
		}, true, nil
	}

	return nil, false, errors.Join(connectionErrors...)
}
