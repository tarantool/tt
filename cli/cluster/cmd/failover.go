package cmd

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/apex/log"
	"github.com/google/uuid"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
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

func makeEtcdOpts(uriOpts UriOpts) libcluster.EtcdOpts {
	opts := libcluster.EtcdOpts{
		Endpoints:      []string{uriOpts.Endpoint},
		Username:       uriOpts.Username,
		Password:       uriOpts.Password,
		KeyFile:        uriOpts.KeyFile,
		CertFile:       uriOpts.CertFile,
		CaPath:         uriOpts.CaPath,
		CaFile:         uriOpts.CaFile,
		SkipHostVerify: uriOpts.SkipHostVerify,
		Timeout:        uriOpts.Timeout,
	}

	return opts
}

// Switch master instance.
func Switch(uri *url.URL, switchCtx SwitchCtx) error {
	uriOpts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	opts := makeEtcdOpts(uriOpts)

	etcd, err := libcluster.ConnectEtcd(opts)
	if err != nil {
		return fmt.Errorf("unable to connect to etcd: %w", err)
	}
	defer etcd.Close()

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
		ctx, cancel_watch := context.WithTimeout(context.Background(),
			time.Duration(switchCtx.Timeout)*time.Second+cmdAdditionalWait)
		outputChan := make(chan *clientv3.Event)
		defer cancel_watch()

		go func() {
			waitChan := etcd.Watch(ctx, key)
			defer close(outputChan)

			for resp := range waitChan {
				for _, ev := range resp.Events {
					switch ev.Type {
					case mvccpb.PUT:
						outputChan <- ev
					}
				}
			}
		}()

		ctx_put, cancel := context.WithTimeout(context.Background(), defaultEtcdTimeout)
		_, err = etcd.Put(ctx_put, key, string(yamlCmd))
		cancel()

		if err != nil {
			return err
		}

		for ev := range outputChan {
			result := switchCmdResult{}
			err = yaml.Unmarshal(ev.Kv.Value, &result)
			if err != nil {
				return err
			}
			fmt.Printf("%s", ev.Kv.Value)
			if result.Status == "success" || result.Status == "failed" {
				return nil
			}
		}
		if ctx.Err() == context.DeadlineExceeded {
			log.Info("Timeout for command execution reached.")
			return nil
		}

		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultEtcdTimeout)
	_, err = etcd.Put(ctx, key, string(yamlCmd))
	cancel()

	if err != nil {
		return err
	}

	fmt.Printf("%s\n%s %s %s\n",
		"To check the switching status, run:",
		"tt cluster failover switch-status",
		uri, uuid)

	return nil
}

// SwitchStatus shows master switching status.
func SwitchStatus(uri *url.URL, switchCtx SwitchStatusCtx) error {
	uriOpts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	opts := makeEtcdOpts(uriOpts)

	etcd, err := libcluster.ConnectEtcd(opts)
	if err != nil {
		return fmt.Errorf("unable to connect to etcd: %w", err)
	}
	defer etcd.Close()

	key := uriOpts.Prefix + failoverPath + switchCtx.TaskID

	ctx, cancel := context.WithTimeout(context.Background(), defaultEtcdTimeout)
	result, err := etcd.Get(ctx, key, clientv3.WithLimit(1))
	cancel()

	if err != nil {
		return err
	}

	if len(result.Kvs) != 1 {
		return fmt.Errorf("task with id `%s` is not found", switchCtx.TaskID)
	}

	fmt.Print(string(result.Kvs[0].Value))

	return nil
}
