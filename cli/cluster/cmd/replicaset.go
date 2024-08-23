package cmd

import (
	"fmt"
	"net/url"
	"time"

	"github.com/apex/log"
	"github.com/manifoldco/promptui"
	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/cli/replicaset"
	libcluster "github.com/tarantool/tt/lib/cluster"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// dataKeyPublisher is a function implements replicaset.DataPublisher.
type dataKeyPublisher func(key string, revision int64, data []byte) error

// Publish helps to satisfy replicaset.DataPublisher interface.
func (publisher dataKeyPublisher) Publish(key string, revision int64, data []byte) error {
	return publisher(key, revision, data)
}

// makeTarantoolPublisher creates publisher that publishes into tarantool.
func makeTarantoolPublisher(factory libcluster.DataPublisherFactory,
	conn tarantool.Connector, prefix string, timeout time.Duration) replicaset.DataPublisher {
	return dataKeyPublisher(func(key string, revision int64, data []byte) error {
		var err error
		key, err = libcluster.GetStorageKey(prefix, key)
		if err != nil {
			return err
		}
		publisher, err := factory.NewTarantool(conn, prefix, key, timeout)
		if err != nil {
			return err
		}
		return publisher.Publish(revision, data)
	})
}

// makeEtcdPublisher creates publisher that publishes into etcd.
func makeEtcdPublisher(factory libcluster.DataPublisherFactory,
	client *clientv3.Client, prefix string, timeout time.Duration) replicaset.DataPublisher {
	return dataKeyPublisher(func(key string, revision int64, data []byte) error {
		var err error
		key, err = libcluster.GetStorageKey(prefix, key)
		if err != nil {
			return err
		}
		publisher, err := factory.NewEtcd(client, prefix, key, timeout)
		if err != nil {
			return err
		}
		return publisher.Publish(revision, data)
	})
}

// PromoteCtx describes the context to promote an instance.
type PromoteCtx struct {
	// InstName is an instance name to promote.
	InstName string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force true if the key selection for patching the config
	// should be skipped.
	Force bool
}

// pickPatchKey prompts to select a key to patch the config.
// If force is true, picks the first passed key.
// If there is only one key prompt not appears. Otherwise
// appears prompt with message of path in config to patch.
func pickPatchKey(keys []string, force bool, pathMsg string) (int, error) {
	if len(keys) == 0 {
		return 0, fmt.Errorf("no keys for the config patching")
	}
	var (
		pos = 0
		err error
	)
	if !force && len(keys) != 1 {
		label := "Select a key for the config patching"
		if len(pathMsg) != 0 {
			label = fmt.Sprintf("%s for destination path %q", label, pathMsg)
		}
		programSelect := promptui.Select{
			Label:        label,
			Items:        keys,
			HideSelected: true,
		}
		pos, _, err = programSelect.Run()
		if err != nil {
			return 0, err
		}
	}
	log.Infof("Patching the config by the key: %q", keys[pos])
	return pos, nil
}

// createDataCollectorAndKeyPublisher creates a new data collector and key publisher.
func createDataCollectorAndKeyPublisher(
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory,
	opts UriOpts, connOpts connectOpts) (
	libcluster.DataCollector, replicaset.DataPublisher, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Key, opts.Timeout
	var (
		collector libcluster.DataCollector
		publisher replicaset.DataPublisher
		closeFunc func()
		err       error
	)
	tarantoolFunc := func(conn tarantool.Connector) error {
		closeFunc = func() { conn.Close() }
		if collectors != nil {
			collector, err = collectors.NewTarantool(conn, prefix, key, timeout)
			if err != nil {
				conn.Close()
				return fmt.Errorf("failed to create tarantool collector: %w", err)
			}
		}
		if publishers != nil {
			publisher = makeTarantoolPublisher(publishers, conn, prefix, timeout)
		}
		return nil
	}
	etcdFunc := func(client *clientv3.Client) error {
		closeFunc = func() { client.Close() }
		if collectors != nil {
			collector, err = collectors.NewEtcd(client, prefix, key, timeout)
			if err != nil {
				client.Close()
				return fmt.Errorf("failed to create etcd collector: %w", err)
			}
		}
		if publishers != nil {
			publisher = makeEtcdPublisher(publishers, client, prefix, timeout)
		}
		return nil
	}

	if err := doOnStorage(connOpts, opts, tarantoolFunc, etcdFunc); err != nil {
		return nil, nil, nil, err
	}

	return collector, publisher, closeFunc, nil
}

// Promote promotes an instance by patching the cluster config.
func Promote(uri *url.URL, ctx PromoteCtx) error {
	opts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}
	connOpts := connectOpts{
		Username: ctx.Username,
		Password: ctx.Password,
	}

	collector, publisher, closeFunc, err := createDataCollectorAndKeyPublisher(
		ctx.Collectors, ctx.Publishers, opts, connOpts)
	if err != nil {
		return err
	}
	defer closeFunc()

	source := replicaset.NewCConfigSource(collector, publisher,
		replicaset.KeyPicker(pickPatchKey))
	err = source.Promote(replicaset.PromoteCtx{
		InstName: ctx.InstName,
		Force:    ctx.Force,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}

// DemoteCtx describes the context to demote an instance.
type DemoteCtx struct {
	// InstName is an instance name to demote.
	InstName string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force true if the key selection for patching the config
	// should be skipped.
	Force bool
}

// Demote demotes an instance by patching the cluster config.
func Demote(uri *url.URL, ctx DemoteCtx) error {
	opts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}
	connOpts := connectOpts{
		Username: ctx.Username,
		Password: ctx.Password,
	}

	collector, publisher, closeFunc, err := createDataCollectorAndKeyPublisher(
		ctx.Collectors, ctx.Publishers, opts, connOpts)
	if err != nil {
		return err
	}
	defer closeFunc()

	source := replicaset.NewCConfigSource(collector, publisher,
		replicaset.KeyPicker(pickPatchKey))
	err = source.Demote(replicaset.DemoteCtx{
		InstName: ctx.InstName,
		Force:    ctx.Force,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}

// ExpelCtx describes the context to expel an instance.
type ExpelCtx struct {
	// InstName is an instance name to demote.
	InstName string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force true if the key selection for patching the config
	// should be skipped.
	Force bool
}

// Expel expels an instance by patching the cluster config.
func Expel(uri *url.URL, ctx ExpelCtx) error {
	opts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}
	connOpts := connectOpts{
		Username: ctx.Username,
		Password: ctx.Password,
	}

	collector, publisher, closeFunc, err := createDataCollectorAndKeyPublisher(
		ctx.Collectors, ctx.Publishers, opts, connOpts)
	if err != nil {
		return err
	}
	defer closeFunc()
	source := replicaset.NewCConfigSource(collector, publisher,
		replicaset.KeyPicker(pickPatchKey))
	err = source.Expel(replicaset.ExpelCtx{
		InstName: ctx.InstName,
		Force:    ctx.Force,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}

// RolesChangeCtx describes the context to add/remove role.
type RolesChangeCtx struct {
	// InstName is an instance name in which add/remove role.
	InstName string
	// GroupName is an replicaset name in which add/remove role.
	GroupName string
	// ReplicasetName is an replicaset name in which add/remove role.
	ReplicasetName string
	// IsGlobal is an boolean value if role needs to add/remove in global scope.
	IsGlobal bool
	// RoleName is a name of role to add/remove.
	RoleName string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force true if the key selection for patching the config
	// should be skipped.
	Force bool
}

// ChangeRole adds/removes a role by patching the cluster config.
func ChangeRole(uri *url.URL, ctx RolesChangeCtx, changeRoleFunc replicaset.ChangeRoleFunc) error {
	opts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}
	connOpts := connectOpts{
		Username: ctx.Username,
		Password: ctx.Password,
	}

	collector, publisher, closeFunc, err := createDataCollectorAndKeyPublisher(
		ctx.Collectors, ctx.Publishers, opts, connOpts)
	if err != nil {
		return err
	}
	defer closeFunc()

	source := replicaset.NewCConfigSource(collector, publisher,
		replicaset.KeyPicker(pickPatchKey))
	err = source.ChangeRole(replicaset.RolesChangeCtx{
		InstName:       ctx.InstName,
		GroupName:      ctx.GroupName,
		ReplicasetName: ctx.ReplicasetName,
		IsGlobal:       ctx.IsGlobal,
		RoleName:       ctx.RoleName,
		Force:          ctx.Force,
	}, changeRoleFunc)
	if err == nil {
		log.Info("Done.")
	}
	return err
}
