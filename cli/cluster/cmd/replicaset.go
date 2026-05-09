package cmd

import (
	"fmt"
	"time"

	"github.com/apex/log"
	"github.com/manifoldco/promptui"
	"github.com/tarantool/go-storage"
	"github.com/tarantool/tt/cli/replicaset"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

// dataKeyPublisher is a function implements replicaset.DataPublisher.
type dataKeyPublisher func(key string, revision int64, data []byte) error

// Publish helps to satisfy replicaset.DataPublisher interface.
func (publisher dataKeyPublisher) Publish(key string, revision int64, data []byte) error {
	return publisher(key, revision, data)
}

// makeStoragePublisher creates publisher for a generic remote storage.
func makeStoragePublisher(factory libcluster.DataPublisherFactory,
	storage storage.Storage, storageType, prefix string, timeout time.Duration,
) replicaset.DataPublisher {
	return dataKeyPublisher(func(key string, revision int64, data []byte) error {
		publisher, err := factory.NewRemoteStorage(storage, prefix, key, timeout, storageType)
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
	opts connect.UriOpts, connOpts libcluster.ConnectOpts) (
	libcluster.DataCollector, replicaset.DataPublisher, func(), error,
) {
	prefix, key, timeout := opts.Prefix, opts.Params["key"], opts.Timeout
	storage, closeFunc, storageType, err := libcluster.NewStorageConnection(connOpts, opts)
	if err != nil {
		return nil, nil, nil, err
	}

	var collector libcluster.DataCollector
	if collectors != nil {
		collector, err = collectors.NewRemoteStorage(storage, prefix, key, timeout, storageType)
		if err != nil {
			closeFunc()
			return nil, nil, nil, fmt.Errorf("failed to create storage collector: %w", err)
		}
	}

	var publisher replicaset.DataPublisher
	if publishers != nil {
		publisher = makeStoragePublisher(publishers, storage, storageType, prefix, timeout)
	}

	return collector, publisher, closeFunc, nil
}

// Promote promotes an instance by patching the cluster config.
func Promote(url string, ctx PromoteCtx) error {
	opts, err := connect.CreateUriOpts(url)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", url, err)
	}
	connOpts := libcluster.ConnectOpts{
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
func Demote(url string, ctx DemoteCtx) error {
	opts, err := connect.CreateUriOpts(url)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", url, err)
	}
	connOpts := libcluster.ConnectOpts{
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
func Expel(url string, ctx ExpelCtx) error {
	opts, err := connect.CreateUriOpts(url)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", url, err)
	}
	connOpts := libcluster.ConnectOpts{
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
func ChangeRole(url string, ctx RolesChangeCtx, action replicaset.RolesChangerAction) error {
	opts, err := connect.CreateUriOpts(url)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", url, err)
	}
	connOpts := libcluster.ConnectOpts{
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
	}, action)
	if err == nil {
		log.Info("Done.")
	}
	return err
}
