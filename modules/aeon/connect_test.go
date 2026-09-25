package aeon_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/aeon"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// clusterYAML is a cluster configuration with a plain router, a router
// whose advertise section names SSL files by relative paths, and an instance
// without aeon. %s is the plain router's advertise URI.
const clusterYAML = `
groups:
  routers:
    roles_cfg:
      aeon.grpc:
        advertise:
          params:
            transport: plain
    replicasets:
      r1:
        instances:
          router-plain:
            roles_cfg:
              aeon.grpc:
                advertise:
                  uri: '%s'
          router-ssl:
            roles_cfg:
              aeon.grpc:
                advertise:
                  uri: 'http://localhost:50051'
                  params:
                    transport: ssl
                    ssl_ca_file: './certs/ca.crt'
  storages:
    replicasets:
      s1:
        instances:
          storage: {}
`

// closedAddress returns a local TCP address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

// cluster returns the cluster configuration whose plain router advertises
// address.
func cluster(address string) string {
	return fmt.Sprintf(clusterYAML, "http://"+address)
}

// pingError is the error tt aeon connect fails with when nothing answers at
// address: the address the arguments resolved to.
func pingError(address string) string {
	return "can't ping to Aeon at " + strconv.Quote(address)
}

// writeFile writes an empty regular file at path.
func writeFile(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, nil, 0o600))
}

// TestConnectURL checks that an aeon URL is connected to as it is, with no
// cluster configuration.
func TestConnectURL(t *testing.T) {
	t.Parallel()

	address := closedAddress(t)

	got := sdktest.Run(t, aeon.New, "aeon", "connect", "http://"+address)

	require.ErrorContains(t, got.Err, pingError(address))
	assert.False(t, got.Exited)
}

// TestConnectApp checks that "app:instance" connects to the address the
// instance advertises in the application's cluster configuration.
func TestConnectApp(t *testing.T) {
	t.Parallel()

	address := closedAddress(t)
	services := sdktest.New(t,
		sdktest.WithClusterConfig(sdk.AppSource("app"), cluster(address)))

	got := services.Run(aeon.New, "aeon", "connect", "app:router-plain")

	require.ErrorContains(t, got.Err, pingError(address))
}

// TestConnectAppSSLPaths checks that the SSL files an application's cluster
// configuration names by relative paths are relative to the directory of
// its cluster config file, not to the working directory.
func TestConnectAppSSLPaths(t *testing.T) {
	t.Parallel()

	appDir := t.TempDir()
	services := sdktest.New(t,
		sdktest.WithClusterConfig(sdk.AppSource("app"), cluster(closedAddress(t))),
		sdktest.WithClusterConfigDir(sdk.AppSource("app"), appDir))

	got := services.Run(aeon.New, "aeon", "connect", "app:router-ssl")

	require.EqualError(t, got.Err,
		`not valid path to trusted certificate authorities (CA) file=`+
			strconv.Quote(filepath.Join(appDir, "certs", "ca.crt")))
}

// TestConnectNotFound checks that an unknown application and an unknown
// instance are not found, before anything is connected to.
func TestConnectNotFound(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"app:missing", "other:router-plain"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			services := sdktest.New(t,
				sdktest.WithClusterConfig(sdk.AppSource("app"), cluster(closedAddress(t))))

			got := services.Run(aeon.New, "aeon", "connect", arg)

			require.ErrorIs(t, got.Err, sdk.ErrNotFound)
			assert.NotContains(t, got.Err.Error(), "ping")
		})
	}
}

// TestConnectNoAeon checks that an instance without an aeon.grpc advertise
// section is an error.
func TestConnectNoAeon(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t,
		sdktest.WithClusterConfig(sdk.AppSource("app"), cluster(closedAddress(t))))

	got := services.Run(aeon.New, "aeon", "connect", "app:storage")

	require.ErrorContains(t, got.Err, "failed to get aeon advertise config")
}

// TestConnectFile checks that a cluster configuration file and an instance
// connect to the address the instance advertises, and that the SSL files the
// file names by relative paths are relative to the file's directory.
func TestConnectFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeFile(t, path)

	address := closedAddress(t)
	newServices := func(t *testing.T) *sdktest.Services {
		t.Helper()

		return sdktest.New(t, sdktest.WithClusterConfig(sdk.FileSource(path), cluster(address)))
	}

	got := newServices(t).Run(aeon.New, "aeon", "connect", path, "router-plain")
	require.ErrorContains(t, got.Err, pingError(address))

	got = newServices(t).Run(aeon.New, "aeon", "connect", path, "router-ssl")
	require.EqualError(t, got.Err,
		`not valid path to trusted certificate authorities (CA) file=`+
			strconv.Quote(filepath.Join(dir, "certs", "ca.crt")))

	// A file the flags name takes the place of the configuration's: it
	// passes the validation and, being empty, fails to load as a CA file.
	caFile := filepath.Join(dir, "flag-ca.crt")
	writeFile(t, caFile)

	got = newServices(t).Run(aeon.New, "aeon", "connect", "--sslcafile", caFile,
		path, "router-ssl")
	require.EqualError(t, got.Err, "not tls config: failed to append CA data")
}

// TestConnectUnrecognized checks that two arguments whose first is neither a
// storage URI nor a file are refused.
func TestConnectUnrecognized(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, aeon.New, "aeon", "connect",
		filepath.Join(t.TempDir(), "missing.yml"), "router-plain")

	require.EqualError(t, got.Err,
		"failed to recognize a connect destination, see the command examples")
}

// TestConnectStorage checks that a storage URI and an instance read the
// storage with the credentials the flags give.
func TestConnectStorage(t *testing.T) {
	t.Parallel()

	const uri = "http://localhost:2379/prefix"

	address := closedAddress(t)
	creds := sdk.Credentials{Username: "user", Password: "secret"}
	services := sdktest.New(t,
		sdktest.WithClusterConfig(sdk.StorageSource(uri, creds), cluster(address)))

	got := services.Run(aeon.New, "aeon", "connect", "-u", "user", "-p", "secret",
		uri, "router-plain")

	require.ErrorContains(t, got.Err, pingError(address))
}

// TestConnectSSLFlags checks the validation of the SSL flags.
func TestConnectSSLFlags(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	key := filepath.Join(dir, "client.key")
	cert := filepath.Join(dir, "client.crt")
	missing := filepath.Join(dir, "missing")

	writeFile(t, key)
	writeFile(t, cert)

	tests := []struct {
		name string
		args []string
		err  string
	}{
		{
			name: "unknown transport",
			args: []string{"--transport", "mode"},
			err:  `invalid argument "mode" for "--transport" flag: must be [plain ssl]`,
		},
		{
			name: "key without a certificate",
			args: []string{"--transport", "ssl", "--sslkeyfile", key},
			err:  "files Key and Cert must be specified both",
		},
		{
			name: "missing key",
			args: []string{"--sslkeyfile", missing, "--sslcertfile", cert},
			err:  "not valid path to a private SSL key file=" + strconv.Quote(missing),
		},
		{
			name: "missing certificate",
			args: []string{"--sslkeyfile", key, "--sslcertfile", missing},
			err:  "not valid path to an SSL certificate file=" + strconv.Quote(missing),
		},
		{
			name: "a CA file implies ssl",
			args: []string{"--sslcafile", missing},
			err: "not valid path to trusted certificate authorities (CA) file=" +
				strconv.Quote(missing),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"aeon", "connect"}, test.args...)

			args = append(args, "http://"+closedAddress(t))

			got := sdktest.Run(t, aeon.New, args...)

			require.EqualError(t, got.Err, test.err)
		})
	}

	// The plain transport ignores the SSL flags.
	address := closedAddress(t)

	got := sdktest.Run(t, aeon.New, "aeon", "connect", "--transport", "plain",
		"--sslcafile", missing, "http://"+address)

	require.ErrorContains(t, got.Err, pingError(address))
}
