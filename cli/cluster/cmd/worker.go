package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	libconnect "github.com/tarantool/tt/lib/connect"
)

// WorkerPublishCtx contains information about cluster worker publish command
// execution context.
type WorkerPublishCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force defines whether the publish should be forced.
	Force bool
	// Src is a raw data to publish.
	Src []byte
}

// WorkerShowCtx contains information about cluster worker show command
// execution context.
type WorkerShowCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
}

// WorkerDeleteCtx contains information about cluster worker delete command
// execution context.
type WorkerDeleteCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force defines whether the delete should be forced (skip confirmation).
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
		return "", "", "", errors.New("URL path must not be empty")
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

func firstNonEmpty(candidates ...string) string {
	for _, s := range candidates {
		if s != "" {
			return s
		}
	}
	return ""
}

// ResolveWorkerCredentials resolves credentials with the priority:
// environment variables < command flags < URL credentials.
func ResolveWorkerCredentials(
	uriOpts libconnect.UriOpts,
	flagUsername, flagPassword string,
) (username, password string) {
	username = firstNonEmpty(
		uriOpts.Username,
		flagUsername,
		os.Getenv(libconnect.EtcdUsernameEnv),
		os.Getenv(libconnect.TarantoolUsernameEnv),
	)

	password = firstNonEmpty(
		uriOpts.Password,
		flagPassword,
		os.Getenv(libconnect.EtcdPasswordEnv),
		os.Getenv(libconnect.TarantoolPasswordEnv),
	)

	return username, password
}

// WorkerPublish publishes a worker configuration. Unimplemented.
func WorkerPublish(url string, ctx WorkerPublishCtx) error {
	return errors.New("unimplemented")
}

// WorkerShow shows a worker configuration. Unimplemented.
func WorkerShow(url string, ctx WorkerShowCtx) error {
	return errors.New("unimplemented")
}

// WorkerDelete deletes a worker configuration. Unimplemented.
func WorkerDelete(url string, ctx WorkerDeleteCtx) error {
	return errors.New("unimplemented")
}
