package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/apex/log"
	"github.com/google/uuid"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
	"gopkg.in/yaml.v2"
)

const (
	defaultEtcdTimeout = 3 * time.Second
	cmdAdditionalWait  = 5 * time.Second
	failoverPath       = "/failover/command/"
)

type switchCmd struct {
	Command   string `yaml:"command"`
	NewMaster string `yaml:"new_master"`
	Timeout   uint64 `yaml:"timeout"`
}

type switchCmdResult struct {
	OldMaster string `yaml:"old_master"`
	TakenBy   struct {
		Active   bool   `yaml:"active"`
		Term     int    `yaml:"term"`
		UUID     string `yaml:"uuid"`
		Hostname string `yaml:"hostname"`
		Pid      int    `yaml:"pid"`
	} `yaml:"taken_by"`
	Timeout        int    `yaml:"timeout"`
	NewMaster      string `yaml:"new_master"`
	Command        string `yaml:"command"`
	Status         string `yaml:"status"`
	Result         string `yaml:"result"`
	ReplicasetName string `yaml:"replicaset_name"`
}

// SwitchCtx describes the context to switch the master instance.
type SwitchCtx struct {
	// InstName is an instance name to switch the master.
	InstName string
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Wait for the command to complete execution.
	Wait bool
	// Timeout for command execution.
	Timeout uint64
}

// SwitchStatus describes the context to the master switching status.
type SwitchStatusCtx struct {
	// Task ID.
	TaskID string
}

// failoverStorage wraps a *RawStorage with the connection cleanup so callers
// can `defer conn.Close()` and have both the storage handle and the underlying
// connection released.
type failoverStorage struct {
	*libcluster.RawStorage
	cleanup func()
}

// Close releases the underlying storage connection.
func (s *failoverStorage) Close() error {
	if err := s.RawStorage.Close(); err != nil {
		s.cleanup()
		return err
	}
	s.cleanup()
	return nil
}

// connectFailoverStorage dials the config storage at uriOpts and returns a
// RawStorage scoped to the configured prefix. Used by the failover commands
// which talk to the storage directly via Get/Put/Watch.
func connectFailoverStorage(uriOpts connect.UriOpts,
	connOpts libcluster.ConnectOpts,
) (*failoverStorage, error) {
	stor, cleanup, storageType, err := libcluster.NewStorageConnection(connOpts, uriOpts)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to config storage: %w", err)
	}

	// Failover commands live in a sibling namespace to cluster config; bind the
	// storage with an empty objectLocation so keys are not implicitly nested
	// under "/config".
	raw, err := libcluster.NewStorage(stor, uriOpts.Prefix, uriOpts.Timeout, "",
		storageType, nil, "")
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("unable to bind %s storage: %w", storageType, err)
	}
	return &failoverStorage{RawStorage: raw, cleanup: cleanup}, nil
}

// Switch master instance.
func Switch(url string, switchCtx SwitchCtx) error {
	uriOpts, err := connect.CreateUriOpts(url)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", url, err)
	}
	connOpts := libcluster.ConnectOpts{
		Username: switchCtx.Username,
		Password: switchCtx.Password,
	}

	conn, err := connectFailoverStorage(uriOpts, connOpts)
	if err != nil {
		return err
	}
	defer conn.Close()

	cmd := switchCmd{
		Command:   "switch",
		NewMaster: switchCtx.InstName,
		Timeout:   switchCtx.Timeout,
	}

	yamlCmd, err := yaml.Marshal(&cmd)
	if err != nil {
		return err
	}

	uuid := uuid.New().String()
	key := uriOpts.Prefix + failoverPath + uuid

	if switchCtx.Wait {
		ctxWatch, cancelWatch := context.WithTimeout(context.Background(),
			time.Duration(switchCtx.Timeout)*time.Second+cmdAdditionalWait)
		defer cancelWatch()
		watchChan, err := conn.Watch(ctxWatch, key)
		if err != nil {
			return fmt.Errorf("unable to create watch channel: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), defaultEtcdTimeout)
		err = conn.Put(ctx, key, string(yamlCmd))
		cancel()

		if err != nil {
			return err
		}

		for ev := range watchChan {
			var result switchCmdResult
			err = yaml.Unmarshal(ev.Value, &result)
			if err != nil {
				return err
			}
			fmt.Printf("%s", ev.Value)
			if result.Status == "success" || result.Status == "failed" {
				return nil
			}
		}
		if ctxWatch.Err() == context.DeadlineExceeded {
			log.Info("Timeout for command execution reached.")
			return nil
		}

		return fmt.Errorf("unexpected problem with watch context: %w", ctxWatch.Err())
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultEtcdTimeout)
	err = conn.Put(ctx, key, string(yamlCmd))
	cancel()

	if err != nil {
		return err
	}

	fmt.Printf("%s\n%s '%s' %s\n",
		"To check the switching status, run:",
		"tt cluster failover switch-status",
		url, uuid)

	return nil
}

// SwitchStatus shows master switching status.
func SwitchStatus(url string, switchCtx SwitchStatusCtx) error {
	uriOpts, err := connect.CreateUriOpts(url)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", url, err)
	}
	var connOpts libcluster.ConnectOpts
	conn, err := connectFailoverStorage(uriOpts, connOpts)
	if err != nil {
		return err
	}
	defer conn.Close()

	key := uriOpts.Prefix + failoverPath + switchCtx.TaskID

	ctx, cancel := context.WithTimeout(context.Background(), defaultEtcdTimeout)
	result, err := conn.Get(ctx, key)
	cancel()

	if err != nil {
		return err
	}

	if len(result) != 1 {
		return fmt.Errorf("task with id `%s` is not found", switchCtx.TaskID)
	}

	fmt.Print(string(result[0].Value))

	return nil
}
