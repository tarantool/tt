package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarantool/go-tarantool/v2"
	libconnect "github.com/tarantool/tt/lib/connect"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// EtcdOpts is a way to configure a etcd client.
type EtcdOpts struct {
	// Endpoints a slice of endpoints to connect.
	Endpoints []string
	// Username is a user name for authorization
	Username string
	// Password is a password for authorization
	Password string
	// KeyFile is a path to a private SSL key file.
	KeyFile string
	// CertFile is a path to an SSL certificate file.
	CertFile string
	// CaPath is a path to a trusted certificate authorities (CA) directory.
	CaPath string
	// CaFile is a path to a trusted certificate authorities (CA) file.
	CaFile string
	// SkipHostVerify controls whether a client verifies the server's
	// certificate chain and host name. This is dangerous option so by
	// default it is false.
	SkipHostVerify bool
	// Timeout is a timeout for actions.
	Timeout time.Duration
}

// ConnectEtcd creates a new client object for a etcd from the specified
// options.
func ConnectEtcd(opts EtcdOpts) (*clientv3.Client, error) {
	var tlsConfig *tls.Config = nil
	if opts.KeyFile != "" || opts.CertFile != "" || opts.CaFile != "" ||
		opts.CaPath != "" || opts.SkipHostVerify {
		tlsInfo := transport.TLSInfo{
			CertFile:      opts.CertFile,
			KeyFile:       opts.KeyFile,
			TrustedCAFile: opts.CaFile,
		}

		var err error
		tlsConfig, err = tlsInfo.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("fail to create tls client config: %w", err)
		}

		if opts.CaPath != "" {
			var err error
			tlsConfig.RootCAs, err = loadRootCA(opts.CaPath)
			if err != nil {
				return nil, fmt.Errorf("fail to load CA directory: %w", err)
			}
		}

		if opts.SkipHostVerify {
			tlsConfig.InsecureSkipVerify = true
		}
	}

	return clientv3.New(clientv3.Config{
		Endpoints:   opts.Endpoints,
		DialTimeout: opts.Timeout,
		Username:    opts.Username,
		Password:    opts.Password,
		TLS:         tlsConfig,
		Logger:      zap.NewNop(),
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
	})
}

// EtcdGetter is the interface that wraps get from etcd method.
type EtcdGetter interface {
	// Get retrieves key-value pairs for a key.
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

// EtcdTxnGetter is the interface that adds Txn method to EtcdGetter.
type EtcdTxnGetter interface {
	EtcdGetter
	// Txn creates a transaction.
	Txn(ctx context.Context) clientv3.Txn
}

// EtcdAllCollector collects data from a etcd connection for a whole prefix.
type EtcdAllCollector struct {
	getter  EtcdGetter
	prefix  string
	timeout time.Duration
}

// NewEtcdAllCollector creates a new collector for etcd from the whole prefix.
func NewEtcdAllCollector(getter EtcdGetter, prefix string, timeout time.Duration) EtcdAllCollector {
	return EtcdAllCollector{
		getter:  getter,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified prefix with the
// specified timeout.
func (collector EtcdAllCollector) Collect() ([]Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, CollectEmptyError{"etcd", prefix}
	}

	collected := []Data{}
	for _, kv := range resp.Kvs {
		collected = append(collected, Data{
			Source:   string(kv.Key),
			Value:    kv.Value,
			Revision: kv.ModRevision,
		})
	}

	return collected, nil
}

// EtcdAllCollector collects data from a etcd connection for a whole prefix
// with integrity checks.
type IntegrityEtcdAllCollector struct {
	getter    EtcdTxnGetter
	prefix    string
	checkFunc CheckFunc
	timeout   time.Duration
}

// NewIntegrityEtcdAllCollector creates a new collector for etcd from the
// whole prefix with integrity checks.
func NewIntegrityEtcdAllCollector(checkFunc CheckFunc,
	getter EtcdTxnGetter, prefix string, timeout time.Duration,
) IntegrityEtcdAllCollector {
	return IntegrityEtcdAllCollector{
		getter:    getter,
		prefix:    prefix,
		checkFunc: checkFunc,
		timeout:   timeout,
	}
}

// Collect collects a configuration from the specified prefix with the
// specified timeout.
func (collector IntegrityEtcdAllCollector) Collect() ([]Data, error) {
	valuesPrefix := getConfigPrefix(collector.prefix)
	hashesPrefix := getHashesPrefix(collector.prefix)
	signsPrefix := getSignPrefix(collector.prefix)

	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Txn(ctx).Then(
		clientv3.OpGet(valuesPrefix, clientv3.WithPrefix()),
		clientv3.OpGet(hashesPrefix, clientv3.WithPrefix()),
		clientv3.OpGet(signsPrefix, clientv3.WithPrefix()),
	).Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	if len(resp.Responses) != 3 {
		return nil,
			fmt.Errorf("failed to fetch data from etcd: some of transaction responses are missing")
	}

	values := resp.Responses[0].GetResponseRange().Kvs
	hashes := resp.Responses[1].GetResponseRange().Kvs
	sigs := resp.Responses[2].GetResponseRange().Kvs

	type valueNode struct {
		Value  []byte
		Hashes map[string][]byte
		Sig    []byte
	}
	keys := []string{}
	nodes := map[string]valueNode{}

	for _, data := range values {
		if key, ok := strings.CutPrefix(string(data.Key), valuesPrefix); ok {
			value := data.Value

			keys = append(keys, key)
			nodes[key] = valueNode{Value: value}
		}
	}

	for _, data := range hashes {
		if ok, hash, key := parseHashPath(string(data.Key), collector.prefix); ok {
			if node, ok := nodes[key]; ok {
				if node.Hashes == nil {
					node.Hashes = map[string][]byte{hash: data.Value}
				} else {
					node.Hashes[hash] = data.Value
				}
				nodes[key] = node
			}
		}
	}

	for _, data := range sigs {
		if key, ok := strings.CutPrefix(string(data.Key), signsPrefix); ok {
			if node, ok := nodes[key]; ok {
				node.Sig = data.Value
				nodes[key] = node
			}
		} else {
			return nil, fmt.Errorf("missing signature for key %q",
				getConfigKey(collector.prefix, key))
		}
	}

	data := []Data{}

	for _, key := range keys {
		node := nodes[key]
		fullKey := getConfigKey(collector.prefix, key)

		err := collector.checkFunc(node.Value, node.Hashes, node.Sig)
		if err != nil {
			return nil, fmt.Errorf("failed to perform integrity checks for key %q: %w",
				fullKey, err)
		}

		data = append(data, Data{
			Source: fullKey,
			Value:  node.Value,
		})
	}

	return data, nil
}

// EtcdKeyCollector collects data from a etcd connection for a whole prefix.
type EtcdKeyCollector struct {
	getter  EtcdGetter
	prefix  string
	key     string
	timeout time.Duration
}

// NewEtcdKeyCollector creates a new collector for etcd from a key from
// a prefix.
func NewEtcdKeyCollector(getter EtcdGetter, prefix, key string,
	timeout time.Duration,
) EtcdKeyCollector {
	return EtcdKeyCollector{
		getter:  getter,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified
// timeout.
func (collector EtcdKeyCollector) Collect() ([]Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	key := prefix + collector.key
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	switch {
	case len(resp.Kvs) == 0:
		// It should not happen, but we need to be sure to avoid a null pointer
		// dereference.
		return nil, fmt.Errorf("a configuration data not found in etcd for key %q",
			key)
	case len(resp.Kvs) > 1:
		return nil, fmt.Errorf("too many responses (%v) from etcd for key %q",
			resp.Kvs, key)
	}

	collected := []Data{
		{
			Source:   string(resp.Kvs[0].Key),
			Value:    resp.Kvs[0].Value,
			Revision: resp.Kvs[0].ModRevision,
		},
	}
	return collected, nil
}

type IntegrityEtcdKeyCollector struct {
	getter    EtcdTxnGetter
	prefix    string
	key       string
	checkFunc CheckFunc
	timeout   time.Duration
}

// NewIntegrityEtcdKeyCollector creates a new collector for etcd with
// additional integrity checks from a key from a prefix.
func NewIntegrityEtcdKeyCollector(checkFunc CheckFunc,
	getter EtcdTxnGetter, prefix, key string,
	timeout time.Duration,
) IntegrityEtcdKeyCollector {
	return IntegrityEtcdKeyCollector{
		getter:    getter,
		prefix:    prefix,
		key:       key,
		checkFunc: checkFunc,
		timeout:   timeout,
	}
}

// Collect collects a configuration from the specified prefix with the
// specified timeout.
func (collector IntegrityEtcdKeyCollector) Collect() ([]Data, error) {
	valueKey := getConfigKey(collector.prefix, collector.key)
	hashesPrefix := getHashesPrefix(collector.prefix)
	sigKey := getSignKey(collector.prefix, collector.key)

	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Txn(ctx).Then(
		clientv3.OpGet(valueKey),
		clientv3.OpGet(hashesPrefix, clientv3.WithPrefix()),
		clientv3.OpGet(sigKey),
	).Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	if len(resp.Responses) != 3 {
		return nil,
			fmt.Errorf("failed to fetch data from etcd: some of transaction responses are missing")
	}

	values := resp.Responses[0].GetResponseRange().Kvs
	hashes := resp.Responses[1].GetResponseRange().Kvs
	sigs := resp.Responses[2].GetResponseRange().Kvs

	switch {
	case len(values) == 0:
		return nil, fmt.Errorf("a configuration data not found in etcd for key %q", valueKey)
	case len(values) > 1:
		return nil, fmt.Errorf("too many responses (%v) from etcd for key %q", values, valueKey)
	case len(hashes) == 0:
		return nil, fmt.Errorf("hashes not found in etcd")
	case len(sigs) == 0:
		return nil, fmt.Errorf("signature not found in etcd for key %q", valueKey)
	case len(sigs) > 1:
		return nil, fmt.Errorf("too many signatures (%v) from etcd for key %q", sigs, valueKey)
	}

	value := values[0].Value
	hashMap := make(map[string][]byte)
	for _, resp := range hashes {
		ok, hash, key := parseHashPath(string(resp.Key), collector.prefix)
		if ok && key == collector.key {
			hashMap[hash] = resp.Value
		}
	}
	sig := sigs[0].Value

	err = collector.checkFunc(value, hashMap, sig)
	if err != nil {
		return nil, fmt.Errorf("failed to perform integrity check for key %q: %w",
			valueKey, err)
	}

	return []Data{{
		Source: valueKey,
		Value:  value,
	}}, nil
}

// EtcdAllDataPublisher publishes a data into etcd to a prefix.
type EtcdAllDataPublisher struct {
	getter  EtcdTxnGetter
	prefix  string
	timeout time.Duration
}

// NewEtcdAllDataPublisher creates a new EtcdAllDataPublisher object to publish
// a data to etcd with the prefix during the timeout.
func NewEtcdAllDataPublisher(getter EtcdTxnGetter,
	prefix string, timeout time.Duration,
) EtcdAllDataPublisher {
	return EtcdAllDataPublisher{
		getter:  getter,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix.
func (publisher EtcdAllDataPublisher) Publish(revision int64, data []byte) error {
	if revision != 0 {
		return fmt.Errorf("failed to publish data into etcd: target revision %d is not supported",
			revision)
	}
	if data == nil {
		return fmt.Errorf("failed to publish data into etcd: data does not exist")
	}

	prefix := getConfigPrefix(publisher.prefix)
	key := prefix + "all"
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	for true {
		// The code tries to put data with the key and remove all other
		// data with the prefix. We need to remove all other data with the
		// prefix because actually the cluster config could be split
		// into several parts with the same prefix.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// First of all we need to get all paths with the prefix.
		resp, err := publisher.getter.Get(ctx, prefix, clientv3.WithPrefix())
		if err != nil {
			return fmt.Errorf("failed to fetch data from etcd: %w", err)
		}

		// Then we need to delete all other paths and put the configuration
		// into the target key. We do it in a single transaction to avoid
		// concurrent updates and collisions.

		// Fill the delete part of the transaction.
		var (
			keys      []string
			revisions []int64
		)
		for _, kv := range resp.Kvs {
			// We need to skip the target key since etcd transactions do not
			// support delete + put for the same key on a single transaction.
			if string(kv.Key) != key {
				keys = append(keys, string(kv.Key))
				revisions = append(revisions, kv.ModRevision)
			}
		}

		var (
			cmps []clientv3.Cmp
			opts []clientv3.Op
		)
		for i, key := range keys {
			cmp := clientv3.Compare(clientv3.ModRevision(key), "=", revisions[i])
			cmps = append(cmps, cmp)
			opts = append(opts, clientv3.OpDelete(key))
		}

		// Fill the put part of the transaction.
		opts = append(opts, clientv3.OpPut(key, string(data)))
		txn := publisher.getter.Txn(ctx)
		if len(cmps) > 0 {
			txn = txn.If(cmps...)
		}

		// And try to execute the transaction.
		tresp, err := txn.Then(opts...).Commit()
		if err != nil {
			return fmt.Errorf("failed to put data into etcd: %w", err)
		}
		if tresp != nil && tresp.Succeeded {
			return nil
		}
	}
	// Unreachable.
	return nil
}

// EtcdAllDataPublisher publishes a signed data into etcd to a prefix.
type IntegrityEtcdAllDataPublisher struct {
	getter   EtcdTxnGetter
	prefix   string
	signFunc SignFunc
	timeout  time.Duration
}

// NewIntegrityEtcdAllDataPublisher creates a new IntegrityEtcdAllDataPublisher
// object to publish a signed data to etcd with the prefix during the timeout.
func NewIntegrityEtcdAllDataPublisher(signFunc SignFunc,
	getter EtcdTxnGetter,
	prefix string, timeout time.Duration,
) IntegrityEtcdAllDataPublisher {
	return IntegrityEtcdAllDataPublisher{
		getter:   getter,
		prefix:   prefix,
		signFunc: signFunc,
		timeout:  timeout,
	}
}

// Publish publishes the configuration into Tarantool to the given prefix.
func (publisher IntegrityEtcdAllDataPublisher) Publish(revision int64, data []byte) error {
	if revision != 0 {
		return fmt.Errorf("failed to publish data into etcd: target revision %d is not supported",
			revision)
	}
	if data == nil {
		return fmt.Errorf("failed to publish data into etcd: data does not exist")
	}

	hashes, sig, err := publisher.signFunc(data)
	if err != nil {
		return err
	}

	const key = "all"
	valueKey := getConfigKey(publisher.prefix, key)
	hashKeys := make(map[string][]byte, len(hashes))
	for k, v := range hashes {
		hashKeys[getHashesKey(publisher.prefix, k, key)] = v
	}
	sigKey := getSignKey(publisher.prefix, key)

	valuePrefix := getConfigPrefix(publisher.prefix)
	hashesPrefix := getHashesPrefix(publisher.prefix)
	sigsPrefix := getSignPrefix(publisher.prefix)

	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	// Start by getting set of currently available keys.
	// If first iteration doesn't succeed, this will be a CAS-loop.
	txnGetOps := []clientv3.Op{
		clientv3.OpGet(valuePrefix, clientv3.WithPrefix()),
		clientv3.OpGet(hashesPrefix, clientv3.WithPrefix()),
		clientv3.OpGet(sigsPrefix, clientv3.WithPrefix()),
	}
	resp, err := publisher.getter.Txn(ctx).Then(txnGetOps...).Commit()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return fmt.Errorf("failed to fetch data from etcd: %w", err)
		}

		if len(resp.Responses) != 3 {
			return fmt.Errorf(
				"failed to fetch data from etcd: some of transaction responses are missing")
		}

		values := resp.Responses[0].GetResponseRange().Kvs
		hashes := resp.Responses[1].GetResponseRange().Kvs
		sigs := resp.Responses[2].GetResponseRange().Kvs

		// Gathering the keys and their revisions.
		deleteKeys := []string{}
		deleteRevisions := []int64{}

		for _, value := range values {
			if string(value.Key) != valueKey {
				deleteKeys = append(deleteKeys, string(value.Key))
				deleteRevisions = append(deleteRevisions, value.ModRevision)
			}
		}

		for _, hash := range hashes {
			found := false
			for k := range hashKeys {
				if string(hash.Key) == k {
					found = true
					break
				}
			}
			if !found {
				deleteKeys = append(deleteKeys, string(hash.Key))
				deleteRevisions = append(deleteRevisions, hash.ModRevision)
			}
		}

		for _, sig := range sigs {
			if string(sig.Key) != sigKey {
				deleteKeys = append(deleteKeys, string(sig.Key))
				deleteRevisions = append(deleteRevisions, sig.ModRevision)
			}
		}

		// Construct the delete part of the transaction
		cmps := []clientv3.Cmp{}
		txnPutOps := []clientv3.Op{}

		for i, key := range deleteKeys {
			rev := deleteRevisions[i]

			cmps = append(cmps, clientv3.Compare(clientv3.ModRevision(key), "=", rev))
			txnPutOps = append(txnPutOps, clientv3.OpDelete(key))
		}

		// Add ops for writing a new value.
		txnPutOps = append(txnPutOps,
			clientv3.OpPut(valueKey, string(data)),
			clientv3.OpPut(sigKey, string(sig)))
		for k, v := range hashKeys {
			txnPutOps = append(txnPutOps, clientv3.OpPut(k, string(v)))
		}

		// Construct and execute a transaction. Otherwise, we'll get a new set of keys.
		resp, err = publisher.getter.Txn(ctx).
			If(cmps...).
			Then(txnPutOps...).
			Else(txnGetOps...).
			Commit()
		if err != nil {
			return fmt.Errorf("failed to put data into etcd: %w", err)
		}

		// If everything OK, we're done.
		if resp.Succeeded {
			break
		}

		// Otherwise, go on to the next CAS iteration.
	}

	return nil
}

// EtcdKeyDataPublisher publishes a data into etcd for a prefix and a key.
type EtcdKeyDataPublisher struct {
	getter   EtcdTxnGetter
	prefix   string
	key      string
	signFunc SignFunc
	timeout  time.Duration
}

// NewEtcdKeyDataPublisher creates a new EtcdKeyDataPublisher object to publish
// a data to etcd with the prefix and key during the timeout.
func NewEtcdKeyDataPublisher(getter EtcdTxnGetter,
	prefix, key string, timeout time.Duration,
) EtcdKeyDataPublisher {
	return EtcdKeyDataPublisher{
		getter:  getter,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// NewIntegrityEtcdKeyDataPublisher creates a new EtcdKeyDataPublisher object
// to publish a signed data to etcd with the prefix and key during the timeout.
func NewIntegrityEtcdKeyDataPublisher(signFunc SignFunc,
	getter EtcdTxnGetter,
	prefix, key string, timeout time.Duration,
) EtcdKeyDataPublisher {
	return EtcdKeyDataPublisher{
		getter:   getter,
		prefix:   prefix,
		key:      key,
		signFunc: signFunc,
		timeout:  timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix and key.
// If passed revision is not 0, the data will be published only if target key revision the same.
func (publisher EtcdKeyDataPublisher) Publish(revision int64, data []byte) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into etcd: data does not exist")
	}

	prefix := getConfigPrefix(publisher.prefix)
	key := prefix + publisher.key
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	txn := publisher.getter.Txn(ctx)
	if revision != 0 {
		txn = txn.If(clientv3.Compare(clientv3.ModRevision(key), "=", revision))
	}

	putOps := []clientv3.Op{}
	putOps = append(putOps, clientv3.OpPut(key, string(data)))

	if publisher.signFunc != nil {
		hashes, sign, err := publisher.signFunc(data)
		if err != nil {
			return fmt.Errorf("failed to sign data: %w", err)
		}
		for hash, value := range hashes {
			targetKey := getHashesKey(publisher.prefix, hash, publisher.key)
			putOps = append(putOps, clientv3.OpPut(targetKey, string(value)))
		}
		putOps = append(putOps,
			clientv3.OpPut(getSignKey(publisher.prefix, publisher.key), string(sign)))
	}

	tresp, err := txn.Then(putOps...).Commit()
	if err != nil {
		return fmt.Errorf("failed to put data into etcd: %w", err)
	}
	if !tresp.Succeeded {
		return fmt.Errorf("failed to put data into etcd: wrong revision")
	}

	return nil
}

// loadRootCA and the code below is a copy-paste from Go sources. We need an
// ability to load ceritificates from a directory, but there is no such public
// function in `crypto`.
//
// https://cs.opensource.google/go/go/+/refs/tags/go1.20.7:src/crypto/x509/root_unix.go;l=62-74
func loadRootCA(path string) (*x509.CertPool, error) {
	roots := x509.NewCertPool()

	fis, err := readUniqueDirectoryEntries(path)
	if err != nil {
		return nil, err
	}

	rootsLen := 0
	for _, fi := range fis {
		data, err := os.ReadFile(path + "/" + fi.Name())
		if err == nil {
			rootsLen++
			roots.AppendCertsFromPEM(data)
		}
	}

	return roots, nil
}

// readUniqueDirectoryEntries is like os.ReadDir but omits
// symlinks that point within the directory.
//
// https://cs.opensource.google/go/go/+/refs/tags/go1.20.7:src/crypto/x509/root_unix.go;l=84-98
func readUniqueDirectoryEntries(dir string) ([]fs.DirEntry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	uniq := files[:0]
	for _, f := range files {
		if !isSameDirSymlink(f, dir) {
			uniq = append(uniq, f)
		}
	}

	return uniq, nil
}

// isSameDirSymlink reports whether fi in dir is a symlink with a
// target not containing a slash.
//
// https://cs.opensource.google/go/go/+/refs/tags/go1.20.7:src/crypto/x509/root_unix.go;l=100-108
func isSameDirSymlink(f fs.DirEntry, dir string) bool {
	if f.Type()&fs.ModeSymlink == 0 {
		return false
	}

	target, err := os.Readlink(filepath.Join(dir, f.Name()))
	return err == nil && !strings.Contains(target, "/")
}

// ConnectOpts is additional connect options specified by a user.
type ConnectOpts struct {
	Username string
	Password string
}

// connectEtcd establishes a connection to etcd.
func СonnectEtcdUriOpts(
	uriOpts libconnect.UriOpts,
	connOpts ConnectOpts,
) (*clientv3.Client, error) {
	etcdOpts := MakeEtcdOptsFromUriOpts(uriOpts)
	if etcdOpts.Username == "" && etcdOpts.Password == "" {
		etcdOpts.Username = connOpts.Username
		etcdOpts.Password = connOpts.Password
		if etcdOpts.Username == "" {
			etcdOpts.Username = os.Getenv(libconnect.EtcdUsernameEnv)
		}
		if etcdOpts.Password == "" {
			etcdOpts.Password = os.Getenv(libconnect.EtcdPasswordEnv)
		}
	}

	etcdcli, err := ConnectEtcd(etcdOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	return etcdcli, nil
}

// DoOnStorage determines a storage based on the opts.
func DoOnStorage(connOpts ConnectOpts, opts libconnect.UriOpts,
	tarantoolFunc func(tarantool.Connector) error, etcdFunc func(*clientv3.Client) error,
) error {
	etcdcli, errEtcd := СonnectEtcdUriOpts(opts, connOpts)
	if errEtcd == nil {
		return etcdFunc(etcdcli)
	}

	conn, errTarantool := СonnectTarantool(opts, connOpts)
	if errTarantool == nil {
		return tarantoolFunc(conn)
	}

	return fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
		errTarantool, errEtcd)
}

// MakeEtcdOptsFromUriOpts create etcd connect options from URI options.
func MakeEtcdOptsFromUriOpts(src libconnect.UriOpts) EtcdOpts {
	var endpoints []string
	if src.Endpoint != "" {
		endpoints = []string{src.Endpoint}
	}

	return EtcdOpts{
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
