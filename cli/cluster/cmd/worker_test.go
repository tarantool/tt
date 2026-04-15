package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	libconnect "github.com/tarantool/tt/lib/connect"
)

func TestParseWorkerPath(t *testing.T) {
	cases := []struct {
		name           string
		urlPath        string
		expectedPrefix string
		expectedHost   string
		expectedWorker string
		expectedErr    string
	}{
		{
			name:           "simple path",
			urlPath:        "/prefix/host1/worker1",
			expectedPrefix: "/prefix",
			expectedHost:   "host1",
			expectedWorker: "worker1",
		},
		{
			name:           "nested prefix",
			urlPath:        "/tdb-workers/tdb-cluster/host1/http-server-1",
			expectedPrefix: "/tdb-workers/tdb-cluster",
			expectedHost:   "host1",
			expectedWorker: "http-server-1",
		},
		{
			name:           "deeply nested prefix",
			urlPath:        "/a/b/c/d/host/worker",
			expectedPrefix: "/a/b/c/d",
			expectedHost:   "host",
			expectedWorker: "worker",
		},
		{
			name:           "minimal path",
			urlPath:        "/host/worker",
			expectedPrefix: "/",
			expectedHost:   "host",
			expectedWorker: "worker",
		},
		{
			name:           "path with trailing slash",
			urlPath:        "/prefix/host/worker/",
			expectedPrefix: "/prefix",
			expectedHost:   "host",
			expectedWorker: "worker",
		},
		{
			name:        "empty path",
			urlPath:     "",
			expectedErr: "URL path must not be empty",
		},
		{
			name:        "single segment",
			urlPath:     "/worker",
			expectedErr: "URL path must contain at least a host-name and a worker-name",
		},
		{
			name:        "single segment no slash",
			urlPath:     "worker",
			expectedErr: "URL path must contain at least a host-name and a worker-name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix, host, worker, err := ParseWorkerPath(tc.urlPath)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expectedPrefix, prefix)
			require.Equal(t, tc.expectedHost, host)
			require.Equal(t, tc.expectedWorker, worker)
		})
	}
}

func TestBuildWorkerStorageKey(t *testing.T) {
	cases := []struct {
		name        string
		prefix      string
		hostName    string
		workerName  string
		expectedKey string
	}{
		{
			name:        "simple",
			prefix:      "/tdb-workers/tdb-cluster",
			hostName:    "host1",
			workerName:  "http-server-1",
			expectedKey: "/tdb-workers/tdb-cluster/instances/host1/http-server-1",
		},
		{
			name:        "prefix with trailing slash",
			prefix:      "/tdb-workers/tdb-cluster/",
			hostName:    "host1",
			workerName:  "worker1",
			expectedKey: "/tdb-workers/tdb-cluster/instances/host1/worker1",
		},
		{
			name:        "root prefix",
			prefix:      "/",
			hostName:    "host",
			workerName:  "worker",
			expectedKey: "/instances/host/worker",
		},
		{
			name:        "empty prefix",
			prefix:      "",
			hostName:    "host",
			workerName:  "worker",
			expectedKey: "/instances/host/worker",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := BuildWorkerStorageKey(tc.prefix, tc.hostName, tc.workerName)
			require.Equal(t, tc.expectedKey, key)
		})
	}
}

func TestResolveWorkerCredentials(t *testing.T) {
	cases := []struct {
		name            string
		envUsername     string
		envPassword     string
		envEtcdUsername string
		envEtcdPassword string
		flagUsername    string
		flagPassword    string
		urlUsername     string
		urlPassword     string
		expectedUser    string
		expectedPass    string
	}{
		{
			name:         "no credentials",
			expectedUser: "",
			expectedPass: "",
		},
		{
			name:         "env only - tarantool",
			envUsername:  "tarantool_user",
			envPassword:  "tarantool_pass",
			expectedUser: "tarantool_user",
			expectedPass: "tarantool_pass",
		},
		{
			name:            "env only - etcd",
			envEtcdUsername: "etcd_user",
			envEtcdPassword: "etcd_pass",
			expectedUser:    "etcd_user",
			expectedPass:    "etcd_pass",
		},
		{
			name:            "etcd env takes precedence over tarantool",
			envUsername:     "tarantool_user",
			envPassword:     "tarantool_pass",
			envEtcdUsername: "etcd_user",
			envEtcdPassword: "etcd_pass",
			expectedUser:    "etcd_user",
			expectedPass:    "etcd_pass",
		},
		{
			name:         "flags override env",
			envUsername:  "env_user",
			envPassword:  "env_pass",
			flagUsername: "flag_user",
			flagPassword: "flag_pass",
			expectedUser: "flag_user",
			expectedPass: "flag_pass",
		},
		{
			name:         "url overrides flags",
			envUsername:  "env_user",
			envPassword:  "env_pass",
			flagUsername: "flag_user",
			flagPassword: "flag_pass",
			urlUsername:  "url_user",
			urlPassword:  "url_pass",
			expectedUser: "url_user",
			expectedPass: "url_pass",
		},
		{
			name:         "url username only",
			urlUsername:  "url_user",
			flagPassword: "flag_pass",
			expectedUser: "url_user",
			expectedPass: "flag_pass",
		},
		{
			name:         "url password only",
			flagUsername: "flag_user",
			urlPassword:  "url_pass",
			expectedUser: "flag_user",
			expectedPass: "url_pass",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envUsername != "" {
				t.Setenv(libconnect.TarantoolUsernameEnv, tc.envUsername)
			}
			if tc.envPassword != "" {
				t.Setenv(libconnect.TarantoolPasswordEnv, tc.envPassword)
			}
			if tc.envEtcdUsername != "" {
				t.Setenv(libconnect.EtcdUsernameEnv, tc.envEtcdUsername)
			}
			if tc.envEtcdPassword != "" {
				t.Setenv(libconnect.EtcdPasswordEnv, tc.envEtcdPassword)
			}

			uriOpts := libconnect.UriOpts{
				Username: tc.urlUsername,
				Password: tc.urlPassword,
			}

			username, password := ResolveWorkerCredentials(
				uriOpts,
				tc.flagUsername,
				tc.flagPassword,
			)
			require.Equal(t, tc.expectedUser, username)
			require.Equal(t, tc.expectedPass, password)
		})
	}
}

func TestWorkerPublish(t *testing.T) {
	err := WorkerPublish("http://localhost:2379/prefix/host/worker", WorkerPublishCtx{})
	require.EqualError(t, err, "unimplemented")
}

func TestWorkerShow(t *testing.T) {
	err := WorkerShow("http://localhost:2379/prefix/host/worker", WorkerShowCtx{})
	require.EqualError(t, err, "unimplemented")
}

func TestWorkerDelete(t *testing.T) {
	err := WorkerDelete("http://localhost:2379/prefix/host/worker", WorkerDeleteCtx{})
	require.EqualError(t, err, "unimplemented")
}

func TestParseWorkerPathAndBuildKeyIntegration(t *testing.T) {
	cases := []struct {
		urlPath     string
		expectedKey string
	}{
		{
			urlPath:     "/tdb-workers/tdb-cluster/host1/http-server-1",
			expectedKey: "/tdb-workers/tdb-cluster/instances/host1/http-server-1",
		},
		{
			urlPath:     "/prefix/host/worker",
			expectedKey: "/prefix/instances/host/worker",
		},
	}

	for _, tc := range cases {
		t.Run(tc.urlPath, func(t *testing.T) {
			prefix, host, worker, err := ParseWorkerPath(tc.urlPath)
			require.NoError(t, err)
			key := BuildWorkerStorageKey(prefix, host, worker)
			require.Equal(t, tc.expectedKey, key)
		})
	}
}
