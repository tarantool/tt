package xlog

import (
	"bytes"
	"fmt"

	"github.com/google/uuid"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

// schemaSpaceID is the id of the _schema system space: box.space._schema.id,
// BOX_SCHEMA_ID in Tarantool's schema_def.h. A snapshot's rows of this space
// carry, among the rest, the replicaset's UUID and name.
const schemaSpaceID = 272

// _schema keys a snapshot names its replicaset under. Tarantool before 2.11
// stored the replicaset UUID under "cluster"; both spellings are read, the
// way Tarantool's own configuration reads them.
const (
	schemaKeyReplicasetUUID = "replicaset_uuid"
	schemaKeyClusterUUID    = "cluster"
	schemaKeyReplicasetName = "replicaset_name"
)

// SnapshotNames is what a snapshot alone says about whose data it is: the
// instance UUID of its header, the _cluster it was taken with, and the
// replicaset UUID and name its _schema holds.
//
// It is the very set of facts Tarantool's configuration reads before it lets
// an instance start on a data directory (snapshot.get_names), and it reads
// them from the newest snapshot only: the journals after it are replayed by
// box.cfg, which runs after that check has passed. A name a journal gives the
// instance therefore does not count for starting, however surely the replay
// ends with it.
type SnapshotNames struct {
	// InstanceUUID is the UUID the snapshot's header carries -- the instance
	// the data belongs to, and what `tt restore apply --patch-uuid` stamps.
	InstanceUUID string
	// InstanceName is the name of the _cluster row whose UUID is the header's,
	// empty when no row carries that UUID or the row carries no name.
	InstanceName string
	// ReplicasetUUID and ReplicasetName are the _schema entries, empty when
	// the snapshot holds none.
	ReplicasetUUID string
	ReplicasetName string
	// Members is the _cluster the snapshot was taken with.
	Members []ClusterMember
}

// ReadSnapshotNames reads the names and UUIDs a snapshot records, the way
// Tarantool's configuration does before an instance starts on it. Rows past
// _cluster are not read: a snapshot is ordered by space id, and the system
// spaces it needs come first.
func ReadSnapshotNames(path string) (SnapshotNames, error) {
	r, err := reader.Open(path)
	if err != nil {
		return SnapshotNames{}, fmt.Errorf("xlog: read snapshot names: open %q: %w", path, err)
	}

	defer func() { _ = r.Close() }()

	names := SnapshotNames{InstanceUUID: r.Meta().InstanceUUID.String()}

	for row, err := range r.Rows() {
		if err != nil {
			return SnapshotNames{}, fmt.Errorf("xlog: read snapshot names: read %q: %w",
				path, err)
		}

		if row.Type != iproto.IPROTO_INSERT && row.Type != iproto.IPROTO_REPLACE {
			continue
		}

		body, err := format.DecodeDMLBody(row.BodyRaw)
		if err != nil {
			continue
		}

		if body.SpaceID > clusterSpaceID {
			break
		}

		switch body.SpaceID {
		case schemaSpaceID:
			if err := readSchemaName(&names, body.Tuple); err != nil {
				return SnapshotNames{}, fmt.Errorf(
					"xlog: read snapshot names: _schema row lsn %d in %q: %w", row.LSN, path, err)
			}
		case clusterSpaceID:
			member, err := decodeClusterTuple(body.Tuple)
			if err != nil {
				return SnapshotNames{}, fmt.Errorf(
					"xlog: read snapshot names: _cluster row lsn %d in %q: %w", row.LSN, path, err)
			}

			names.Members = append(names.Members, member)
		}
	}

	names.InstanceName = nameOf(names.Members, names.InstanceUUID)

	return names, nil
}

// nameOf returns the name of the member whose UUID is id, compared as UUIDs
// rather than as strings: the header and the _cluster row may spell one UUID
// differently, and Tarantool parses both.
func nameOf(members []ClusterMember, id string) string {
	want, err := uuid.Parse(id)
	if err != nil {
		return ""
	}

	for _, member := range members {
		if got, err := uuid.Parse(member.UUID); err == nil && got == want {
			return member.Name
		}
	}

	return ""
}

// readSchemaName records the replicaset UUID or name a _schema tuple carries.
// The other keys of _schema are left alone, and so is the value of a key this
// reader does not look for: it need not be a string.
func readSchemaName(names *SnapshotNames, tuple []byte) error {
	dec := msgpack.NewDecoder(bytes.NewReader(tuple))

	fields, err := dec.DecodeArrayLen()
	if err != nil {
		return fmt.Errorf("decode tuple: %w", err)
	}

	if fields < 2 {
		return nil
	}

	key, err := dec.DecodeString()
	if err != nil {
		// A _schema key is a string in every Tarantool a restore replays, but
		// a row this reader does not look for is not its to refuse.
		return nil //nolint:nilerr
	}

	var dst *string

	switch key {
	case schemaKeyReplicasetUUID, schemaKeyClusterUUID:
		dst = &names.ReplicasetUUID
	case schemaKeyReplicasetName:
		dst = &names.ReplicasetName
	default:
		return nil
	}

	code, err := dec.PeekCode()
	if err != nil {
		return fmt.Errorf("decode %s: %w", key, err)
	}

	if code == msgpcode.Nil {
		return nil
	}

	if *dst, err = dec.DecodeString(); err != nil {
		return fmt.Errorf("decode %s: %w", key, err)
	}

	return nil
}
