package backup

import (
	"errors"
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/v3/cli/connector"
)

var (
	errEmptyInstanceInfo         = errors.New("instance returned empty info")
	errEmptyClusterMembership    = errors.New("instance returned empty _cluster")
	errClusterMembershipResponse = errors.New("unexpected _cluster response")
	errClusterMemberTuple        = errors.New("unexpected _cluster row")
	errClusterMemberID           = errors.New("unexpected _cluster row id")
	errClusterMemberUUID         = errors.New("unexpected _cluster row uuid")
)

// defaultTTL is the backup lease duration used when the caller does not set one.
const defaultTTL = time.Hour
const clusterTupleMinFields = 2

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

	_,
		err := conn.Eval("box.backup.start(...)",
		[]any{args},
		connector.RequestOpts{PushCallback: nil,
			ReadTimeout: 0,
			ResData:     nil})
	if err != nil {
		return fmt.Errorf("failed to start backup: %w", err)
	}

	return nil
}

// GetInfo runs box.backup.info() and decodes the result. It returns nil if no
// backup is open.
//
//nolint:nilnil // A closed backup has no info and is not an error.
func GetInfo(conn connector.Connector) (*BackupInfo, error) {
	res,
		err := conn.Eval("return box.backup.info()",
		[]any{},
		connector.RequestOpts{PushCallback: nil,
			ReadTimeout: 0,
			ResData:     nil})
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

	decodeErr := decoder.Decode(res[0])
	if decodeErr != nil {
		return nil, fmt.Errorf("failed to parse backup info: %w", decodeErr)
	}

	return &info, nil
}

// stopBackup runs box.backup.stop().
func stopBackup(conn connector.Connector) error {
	_,
		err := conn.Eval("box.backup.stop()",
		[]any{},
		connector.RequestOpts{PushCallback: nil,
			ReadTimeout: 0,
			ResData:     nil})
	if err != nil {
		return fmt.Errorf("failed to stop backup: %w", err)
	}

	return nil
}

// instanceInfoExpr fetches replicaset/instance uuids, instance name and data
// directories in one round-trip. box.info.name may be absent on Tarantool 2.x.
// The _cluster membership is deliberately not read here: the membership a
// fragment records comes from the backup's own frozen range, not from a live read
// that races the freeze -- see fragmentMembership.
const instanceInfoExpr = `return {
	replicaset_uuid = box.info.replicaset.uuid,
	instance_uuid   = box.info.uuid,
	instance_name   = box.info.name,
	hostname        = box.info.hostname,
	wal_dir         = box.cfg.wal_dir,
	memtx_dir       = box.cfg.memtx_dir,
	vinyl_dir       = box.cfg.vinyl_dir,
}`

// GetInstanceInfo fetches instance-identifying fields, the hostname and data
// directories from the instance (box.info/box.cfg).
func GetInstanceInfo(conn connector.Connector) (*InstanceInfo, error) {
	res,
		err := conn.Eval(instanceInfoExpr,
		[]any{},
		connector.RequestOpts{PushCallback: nil,
			ReadTimeout: 0,
			ResData:     nil})
	if err != nil {
		return nil, fmt.Errorf("failed to get instance info: %w", err)
	}

	if len(res) == 0 || res[0] == nil {
		return nil, errEmptyInstanceInfo
	}

	var decoded struct {
		ReplicasetUUID string `json:"replicaset_uuid"`
		InstanceUUID   string `json:"instance_uuid"`
		InstanceName   string `json:"instance_name"`
		Hostname       string `json:"hostname"`
		WalDir         string `json:"wal_dir"`
		MemtxDir       string `json:"memtx_dir"`
		VinylDir       string `json:"vinyl_dir"`
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "json",
		WeaklyTypedInput: true,
		Result:           &decoded,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to decode instance info: %w", err)
	}

	decodeErr2 := decoder.Decode(res[0])
	if decodeErr2 != nil {
		return nil, fmt.Errorf("failed to decode instance info: %w", decodeErr2)
	}

	return &InstanceInfo{
		ReplicasetUUID: decoded.ReplicasetUUID,
		InstanceUUID:   decoded.InstanceUUID,
		InstanceName:   decoded.InstanceName,
		WalDir:         decoded.WalDir,
		MemtxDir:       decoded.MemtxDir,
		VinylDir:       decoded.VinylDir,
		Hostname:       decoded.Hostname,
	}, nil
}

// clusterMembersExpr fetches the full _cluster membership in a round trip of
// its own. The start flow evaluates it only when the backup lists no snapshot
// to take the membership from -- see fragmentMembership. _cluster rows are
// {id, uuid} or {id, uuid, name} tuples, the name being nil on rows written
// by old replicas.
const clusterMembersExpr = `return box.space._cluster:select()`

// GetClusterMembers fetches the _cluster membership from the instance.
func GetClusterMembers(conn connector.Connector) ([]ClusterMember, error) {
	res,
		err := conn.Eval(clusterMembersExpr,
		[]any{},
		connector.RequestOpts{PushCallback: nil,
			ReadTimeout: 0,
			ResData:     nil})
	if err != nil {
		return nil, fmt.Errorf("failed to get _cluster membership: %w", err)
	}

	if len(res) == 0 || res[0] == nil {
		return nil, errEmptyClusterMembership
	}

	rows, ok := res[0].([]any)
	if !ok {
		return nil, fmt.Errorf("%w %v", errClusterMembershipResponse, res[0])
	}

	members, err := decodeClusterMembers(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to decode _cluster membership: %w", err)
	}

	return members, nil
}

// decodeClusterMembers turns box.space._cluster tuples ({id, uuid} or
// {id, uuid, name}) into ClusterMember values. A map decoder cannot read a
// positional tuple, so the rows arrive as raw values and are picked apart
// here; a name that is absent or nil decodes as the empty name.
func decodeClusterMembers(rows []any) ([]ClusterMember, error) {
	members := make([]ClusterMember, 0, len(rows))
	for _, row := range rows {
		tuple, ok := row.([]any)
		if !ok || len(tuple) < clusterTupleMinFields {
			return nil, fmt.Errorf("%w %v", errClusterMemberTuple, row)
		}

		replicaID, err := decodeClusterMemberID(tuple[0])
		if err != nil {
			return nil, err
		}

		memberUUID, err := decodeClusterMemberUUID(tuple[1])
		if err != nil {
			return nil, err
		}

		var member ClusterMember

		member.ID, member.UUID = replicaID, memberUUID

		if len(tuple) > clusterTupleMinFields {
			member.Name, _ = tuple[2].(string)
		}

		members = append(members, member)
	}

	return members, nil
}

// decodeClusterMemberID reads the numeric id of a _cluster row. An id
// arrives as whatever Go integer width the msgpack decoder picked for the
// code it saw -- a fixnum decodes to int8, wider codes to their own width.
func decodeClusterMemberID(value any) (uint32, error) {
	switch replicaID := value.(type) {
	case int:
		return uint32(replicaID), nil //nolint:gosec // The engine supplies uint32 member IDs.
	case int8:
		return uint32(replicaID), nil //nolint:gosec // The engine supplies uint32 member IDs.
	case int16:
		return uint32(replicaID), nil //nolint:gosec // The engine supplies uint32 member IDs.
	case int32:
		return uint32(replicaID), nil //nolint:gosec // The engine supplies uint32 member IDs.
	case int64:
		return uint32(replicaID), nil //nolint:gosec // The engine supplies uint32 member IDs.
	case uint8:
		return uint32(replicaID), nil
	case uint16:
		return uint32(replicaID), nil
	case uint32:
		return replicaID, nil
	case uint64:
		return uint32(replicaID), nil //nolint:gosec // The engine supplies uint32 member IDs.
	default:
		return 0, fmt.Errorf("%w %v", errClusterMemberID, value)
	}
}

// decodeClusterMemberUUID reads the uuid of a _cluster row, which arrives
// as a string or as a value that renders itself as one.
func decodeClusterMemberUUID(value any) (string, error) {
	switch uuid := value.(type) {
	case string:
		return uuid, nil
	case fmt.Stringer:
		return uuid.String(), nil
	default:
		return "", fmt.Errorf("%w %v", errClusterMemberUUID, value)
	}
}
