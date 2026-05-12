package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tarantool/go-storage"
	storageconnect "github.com/tarantool/go-storage/connect"
	"github.com/tarantool/go-storage/operation"
	"github.com/tarantool/go-storage/predicate"
	libconnect "github.com/tarantool/tt/lib/connect"
)

func ConnectStorage(
	opts libconnect.UriOpts,
	username, password string,
) (storage.Storage, func(), error) {
	sslEnable := opts.KeyFile != "" || opts.CertFile != "" ||
		opts.CaFile != "" || opts.CaPath != "" ||
		opts.SkipHostVerify || opts.SkipPeerVerify ||
		strings.HasPrefix(opts.Endpoint, "https://")

	cfg := storageconnect.Config{
		Endpoints:   []string{opts.Endpoint},
		Username:    username,
		Password:    password,
		DialTimeout: opts.Timeout,
		SSL: storageconnect.SSLConfig{
			Enable:     sslEnable,
			CaFile:     opts.CaFile,
			CaPath:     opts.CaPath,
			CertFile:   opts.CertFile,
			KeyFile:    opts.KeyFile,
			Ciphers:    opts.Ciphers,
			VerifyHost: !opts.SkipHostVerify,
			VerifyPeer: !opts.SkipPeerVerify,
		},
	}

	// DialTimeout used to is used to control connection time.
	ctx := context.Background()

	stg, cleanup, err := storageconnect.NewStorage(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to storage: %w", err)
	}
	return stg, cleanup, nil
}

// WorkerPublishCtx contains information about cluster worker publish command
// execution context.
type WorkerPublishCtx struct {
	// Storage is the storage instance for the operation.
	Storage storage.Storage
	// Key is the key in storage for the worker configuration.
	Key string
	// Src is a raw data to publish.
	Src []byte
	// Force defines whether the publish should be forced.
	Force bool
}

// WorkerShowCtx contains information about cluster worker show command
// execution context.
type WorkerShowCtx struct {
	// Storage is the storage instance for the operation.
	Storage storage.Storage
	// Key is the key in storage for the worker configuration.
	Key string
}

// WorkerDeleteCtx contains information about cluster worker delete command
// execution context.
type WorkerDeleteCtx struct {
	// Storage is the storage instance for the operation.
	Storage storage.Storage
	// Key is the key in storage for the worker configuration.
	Key string
	// Force defines whether the delete should be forced (skip existence check).
	Force bool
}

// ParseWorkerPath parses a URL path and extracts prefix, hostName and workerName.
// The expected format is: /prefix/host-name/worker-name
// where the last two segments are host-name and worker-name,
// and everything before them is the prefix.
//
// Example: /tdb-workers/tdb-cluster/host1/http-server-1
// → prefix="/tdb-workers/tdb-cluster", hostName="host1", workerName="http-server-1".
func ParseWorkerPath(urlPath string) (prefix, hostName, workerName string, err error) {
	path := strings.TrimPrefix(urlPath, "/")
	path = strings.TrimSuffix(path, "/")

	if path == "" {
		return "", "", "", fmt.Errorf("URL path must not be empty")
	}

	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf(
			"URL path must contain at least a host-name and a worker-name, got: %q", urlPath)
	}

	workerName = parts[len(parts)-1]
	hostName = parts[len(parts)-2]
	prefix = "/" + strings.Join(parts[:len(parts)-2], "/")

	return prefix, hostName, workerName, nil
}

// BuildWorkerStorageKey builds the storage key for a worker configuration.
// The format is: /<prefix>/instances/<host-name>/<worker-name>.
func BuildWorkerStorageKey(prefix, hostName, workerName string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	return fmt.Sprintf("%s/instances/%s/%s", prefix, hostName, workerName)
}

func ResolveWorkerCredentials(
	uriOpts libconnect.UriOpts,
	flagUsername, flagPassword string,
) (username, password string) {
	if uriOpts.Username != "" || uriOpts.Password != "" {
		return uriOpts.Username, uriOpts.Password
	}

	username = flagUsername
	password = flagPassword
	if username == "" {
		username = os.Getenv(libconnect.EtcdUsernameEnv)
	}
	if username == "" {
		username = os.Getenv(libconnect.TarantoolUsernameEnv)
	}
	if password == "" {
		password = os.Getenv(libconnect.EtcdPasswordEnv)
	}
	if password == "" {
		password = os.Getenv(libconnect.TarantoolPasswordEnv)
	}
	return username, password
}

// WorkerPublish publishes a worker configuration to storage.
// Without Force flag, it atomically checks that the key does not exist and
// publishes the configuration. If the key already exists, an error is returned.
// With Force flag, it overwrites the existing configuration unconditionally.
func WorkerPublish(publishCtx WorkerPublishCtx) error {
	ctx := context.Background()
	key := []byte(publishCtx.Key)
	value := publishCtx.Src

	if publishCtx.Force {
		_, err := publishCtx.Storage.Tx(ctx).Then(operation.Put(key, value)).Commit()
		if err != nil {
			return fmt.Errorf("failed to publish worker configuration: %w", err)
		}
		return nil
	}

	resp, err := publishCtx.Storage.Tx(ctx).
		If(predicate.VersionEqual(key, 0)).
		Then(operation.Put(key, value)).
		Commit()
	if err != nil {
		return fmt.Errorf("failed to publish worker configuration: %w", err)
	}

	if !resp.Succeeded {
		return fmt.Errorf(
			"worker configuration already exists at %q, use --force to overwrite",
			publishCtx.Key)
	}

	return nil
}

// WorkerShow shows a worker configuration from storage.
// It returns an error if the configuration is not found.
func WorkerShow(showCtx WorkerShowCtx) ([]byte, error) {
	ctx := context.Background()
	key := []byte(showCtx.Key)

	resp, err := showCtx.Storage.Tx(ctx).Then(operation.Get(key)).Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to get worker configuration: %w", err)
	}

	if len(resp.Results) == 0 || len(resp.Results[0].Values) == 0 {
		return nil, fmt.Errorf("worker configuration not found at %q", showCtx.Key)
	}

	value := resp.Results[0].Values[0].Value

	return value, nil
}

// WorkerDelete deletes a worker configuration from storage.
// Without Force flag, it atomically checks that the key exists and
// deletes the configuration. If the key does not exist, an error is returned.
// With Force flag, it deletes the configuration unconditionally.
func WorkerDelete(deleteCtx WorkerDeleteCtx) error {
	ctx := context.Background()
	key := []byte(deleteCtx.Key)

	if deleteCtx.Force {
		_, err := deleteCtx.Storage.Tx(ctx).Then(operation.Delete(key)).Commit()
		if err != nil {
			return fmt.Errorf("failed to delete from storage: %w", err)
		}
		return nil
	}

	resp, err := deleteCtx.Storage.Tx(ctx).
		If(predicate.VersionNotEqual(key, 0)).
		Then(operation.Delete(key)).
		Commit()
	if err != nil {
		return fmt.Errorf("failed to delete from storage: %w", err)
	}

	if !resp.Succeeded {
		return fmt.Errorf(
			"worker configuration not found at %q",
			deleteCtx.Key)
	}

	return nil
}
