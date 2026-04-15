package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
)

func TestStorageCredentialsByBackend(t *testing.T) {
	t.Setenv(sdkconnect.EtcdUsernameEnv, "etcd-user")
	t.Setenv(sdkconnect.EtcdPasswordEnv, "etcd-password")
	t.Setenv(sdkconnect.TarantoolUsernameEnv, "tcs-user")
	t.Setenv(sdkconnect.TarantoolPasswordEnv, "tcs-password")

	backends := []struct {
		name     string
		config   func(ConnectOpts, sdkconnect.URIOpts) gsconnect.Config
		username string
		password string
	}{
		{"etcd", getEtcdCfg, "etcd-user", "etcd-password"},
		{"TCS", getTarantoolCfg, "tcs-user", "tcs-password"},
	}

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			cases := []struct {
				name     string
				flags    ConnectOpts
				uri      sdkconnect.URIOpts
				username string
				password string
			}{
				{
					name:     "own backend environment",
					username: backend.username,
					password: backend.password},
				{
					name:     "flags override environment",
					flags:    ConnectOpts{Username: "flag-user", Password: "flag-password"},
					username: "flag-user",
					password: "flag-password"},
				{
					name:     "partial flags username",
					flags:    ConnectOpts{Username: "flag-user"},
					username: "flag-user",
					password: backend.password},
				{
					name:     "partial flags password",
					flags:    ConnectOpts{Password: "flag-password"},
					username: backend.username,
					password: "flag-password"},
				{
					name:     "URL username is a pair",
					uri:      sdkconnect.URIOpts{Username: "uri-user"},
					flags:    ConnectOpts{Username: "flag-user", Password: "flag-password"},
					username: "uri-user"},
				{
					name:     "URL password is a pair",
					uri:      sdkconnect.URIOpts{Password: "uri-password"},
					flags:    ConnectOpts{Username: "flag-user", Password: "flag-password"},
					password: "uri-password"},
			}

			for _, testCase := range cases {
				t.Run(testCase.name, func(t *testing.T) {
					cfg := backend.config(testCase.flags, testCase.uri)
					assert.Equal(t, testCase.username, cfg.Username)
					assert.Equal(t, testCase.password, cfg.Password)
				})
			}
		})
	}
}
