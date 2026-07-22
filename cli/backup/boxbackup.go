package backup

import (
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/connector"
)

// defaultTTL is the backup lease duration used when the caller does not set one.
const defaultTTL = time.Hour

// StartOpts are the parameters of box.backup.start().
type StartOpts struct {
	// FromVclock selects an incremental backup; nil means a full backup.
	FromVclock Vclock
	// TTL is the backup lease duration. Zero falls back to defaultTTL (1h).
	TTL time.Duration
}

// startBackup runs box.backup.startBackup({from_vclock, ttl}) on the instance.
func startBackup(conn connector.Connector, opts StartOpts) error {
	if opts.TTL == 0 {
		opts.TTL = defaultTTL
	}

	args := map[string]any{"ttl": opts.TTL.Seconds()}
	if opts.FromVclock != nil {
		args["from_vclock"] = map[uint32]uint64(opts.FromVclock)
	}

	_, err := conn.Eval("box.backup.start(...)", []any{args}, connector.RequestOpts{})
	if err != nil {
		return fmt.Errorf("failed to start backup: %w", err)
	}

	return nil
}

// GetInfo runs box.backup.info() and decodes the result. It returns nil if no
// backup is open.
func GetInfo(conn connector.Connector) (*BackupInfo, error) {
	res, err := conn.Eval("return box.backup.info()", []any{}, connector.RequestOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get backup info: %w", err)
	}
	if len(res) == 0 || res[0] == nil {
		return nil, nil
	}

	var info BackupInfo
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "json",
		WeaklyTypedInput: true,
		Result:           &info,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse backup info: %w", err)
	}
	if err := decoder.Decode(res[0]); err != nil {
		return nil, fmt.Errorf("failed to parse backup info: %w", err)
	}
	return &info, nil
}

// stopBackup runs box.backup.stop().
func stopBackup(conn connector.Connector) error {
	_, err := conn.Eval("box.backup.stop()", []any{}, connector.RequestOpts{})
	if err != nil {
		return fmt.Errorf("failed to stop backup: %w", err)
	}
	return nil
}

// instanceInfoExpr fetches replicaset/instance uuids, instance name,
// hostname and WAL/memtx directories in one round-trip. box.info.name and
// box.info.hostname may be absent on Tarantool 2.x.
const instanceInfoExpr = `return {
	replicaset_uuid = box.info.replicaset.uuid,
	instance_uuid   = box.info.uuid,
	instance_name   = box.info.name,
	hostname        = box.info.hostname,
	wal_dir         = box.cfg.wal_dir,
	memtx_dir       = box.cfg.memtx_dir,
}`

// GetInstanceInfo fetches instance-identifying fields, hostname and
// WAL/memtx directories from the instance via net.box (box.info/box.cfg).
func GetInstanceInfo(conn connector.Connector) (*InstanceInfo, error) {
	res, err := conn.Eval(instanceInfoExpr, []any{}, connector.RequestOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get instance info: %w", err)
	}
	if len(res) == 0 || res[0] == nil {
		return nil, fmt.Errorf("instance returned empty info")
	}

	var decoded struct {
		ReplicasetUUID string `json:"replicaset_uuid"`
		InstanceUUID   string `json:"instance_uuid"`
		InstanceName   string `json:"instance_name"`
		Hostname       string `json:"hostname"`
		WalDir         string `json:"wal_dir"`
		MemtxDir       string `json:"memtx_dir"`
	}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "json",
		WeaklyTypedInput: true,
		Result:           &decoded,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to decode instance info: %w", err)
	}
	if err := decoder.Decode(res[0]); err != nil {
		return nil, fmt.Errorf("failed to decode instance info: %w", err)
	}
	return &InstanceInfo{
		ReplicasetUUID: decoded.ReplicasetUUID,
		InstanceUUID:   decoded.InstanceUUID,
		InstanceName:   decoded.InstanceName,
		Hostname:       decoded.Hostname,
		WalDir:         decoded.WalDir,
		MemtxDir:       decoded.MemtxDir,
	}, nil
}
