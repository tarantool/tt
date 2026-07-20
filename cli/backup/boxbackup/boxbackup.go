package boxbackup

import (
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/connector"
)

const defaultTTL = time.Hour

// StartOpts are the parameters of box.backup.start().
type StartOpts struct {
	// FromVclock selects an incremental backup.
	FromVclock backup.Vclock // nil means a full backup.
	// TTL is the backup lease duration.
	TTL time.Duration
}

// Info is the decoded result of box.backup.info().
type Info struct {
	Files          []string                `json:"files"`
	Type           backup.BackupType       `json:"type"`
	VclockBegin    backup.Vclock           `json:"vclock_begin"`
	VclockEnd      backup.Vclock           `json:"vclock_end"`
	RecoveryPoints *[]backup.RecoveryPoint `json:"recovery_points"`
}

// Start runs box.backup.start() on the instance.
func Start(conn connector.Connector, opts StartOpts) error {
	ttl := opts.TTL
	if ttl == 0 {
		ttl = defaultTTL
	}

	args := map[string]any{"ttl": ttl.Seconds()}
	if opts.FromVclock != nil {
		args["from_vclock"] = map[uint32]uint64(opts.FromVclock)
	}

	_, err := conn.Eval("box.backup.start(...)", []any{args}, connector.RequestOpts{})
	if err != nil {
		return fmt.Errorf("failed to start backup: %w", err)
	}

	return nil
}

// GetInfo runs box.backup.info().
func GetInfo(conn connector.Connector) (*Info, error) {
	res, err := conn.Eval("return box.backup.info()", []any{}, connector.RequestOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get backup info: %w", err)
	}
	if len(res) == 0 || res[0] == nil {
		return nil, nil
	}

	var info Info
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

// Stop runs box.backup.stop().
func Stop(conn connector.Connector) error {
	_, err := conn.Eval("box.backup.stop()", []any{}, connector.RequestOpts{})
	if err != nil {
		return fmt.Errorf("failed to stop backup: %w", err)
	}

	return nil
}
