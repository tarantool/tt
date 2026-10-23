package xlog

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

var (
	errClusterAnUpdateOfMember = errors.New("an update of member")
	errClusterAnOperation      = errors.New("an operation")
	errClusterAKeyOf           = errors.New("a key of")
	errClusterMemberID         = errors.New("member id")
	errClusterATupleOf         = errors.New("a tuple of")
)

// clusterSpaceID is the id of the _cluster system space: box.space._cluster.id,
// BOX_CLUSTER_ID in Tarantool's schema_def.h. Every Tarantool whose snapshots
// a restore can replay agrees on it, so a snapshot's rows of this space are
// the replicaset membership it was taken with.
const (
	clusterSpaceID          = 320
	clusterTupleMinFields   = 2
	clusterTupleNamedFields = 3
	clusterUpdateFieldCount = 3
	clusterNameFieldNumber  = 3
)

// ClusterMember is one row of _cluster as a snapshot records it.
type ClusterMember struct {
	// ID is the numeric id the replicaset knows the member by.
	ID uint32
	// UUID is the member's instance UUID, in the 36-char form Tarantool
	// stores it in.
	UUID string
	// Name is the member's instance name, empty for a member the replicaset
	// registered without one.
	Name string
}

// ReadCluster returns the _cluster membership a snapshot file records: every
// member the replicaset had when the snapshot was taken, the instance the
// backup was taken on included. The snapshot's other spaces are not decoded
// for their bodies.
func ReadCluster(path string) ([]ClusterMember, error) {
	logReader, err := reader.Open(path)
	if err != nil {
		return nil, fmt.Errorf("xlog: read _cluster: open %q: %w", path, err)
	}

	defer func() { _ = logReader.Close() }()

	var members []ClusterMember

	for row, err := range logReader.Rows() {
		if err != nil {
			return nil, fmt.Errorf("xlog: read _cluster: read %q: %w", path, err)
		}

		// A snapshot holds the current state of every space as whole tuples.
		// The other row types carry no tuple a member can be read from.
		if row.Type != iproto.IPROTO_INSERT && row.Type != iproto.IPROTO_REPLACE {
			continue
		}

		body, err := format.DecodeDMLBody(row.BodyRaw)
		if err != nil || body.SpaceID != clusterSpaceID {
			continue
		}

		member, err := decodeClusterTuple(body.Tuple)
		if err != nil {
			return nil, fmt.Errorf("xlog: read _cluster: row lsn %d in %q: %w",
				row.LSN, path, err)
		}

		members = append(members, member)
	}

	return members, nil
}

// MembershipAt answers: what does a replay of exactly these files end with?
// It folds the _cluster a snapshot carries with the _cluster changes the
// journals after it hold, in the order given -- the membership an instance
// restored onto them boots into, the one restore apply verifies a
// --patch-uuid against. A replace or insert writes the member its tuple
// names, a delete forgets the member its key names, and an update names a
// member the replicaset had registered nameless.
func MembershipAt(snapshot string, journals []string) ([]ClusterMember, error) {
	return foldMembership(snapshot, journals, nil)
}

// MembershipUpTo answers: what does the range the freeze closed end with?
// It is the MembershipAt fold clamped at upto: a row whose LSN is strictly
// greater than upto[row.ReplicaID] is skipped, and a replica id absent from
// upto counts as 0, so every row of a replica the freeze never saw is
// dropped -- that replica wrote nothing inside the frozen range, and any of
// its rows in the files lie past the freeze. The clamp keeps out what a
// pinned journal gains after the freeze: the files keep growing until the
// backup is finalized, and rows past the frozen vclock are outside the
// promise the backup's fragment makes. This is the membership a backup's
// data restores; MembershipAt stays the unclamped answer restore apply uses.
func MembershipUpTo(
	snapshot string,
	journals []string,
	upto format.VClock,
) ([]ClusterMember, error) {
	return foldMembership(snapshot, journals, upto)
}

// foldMembership seeds the membership from the snapshot's _cluster rows and
// folds the journals in the order given, skipping the rows behind the
// snapshot's vclock and -- when upto is not nil -- the rows past it. A nil
// upto is the unclamped MembershipAt fold.
func foldMembership(
	snapshot string,
	journals []string,
	upto format.VClock,
) ([]ClusterMember, error) {
	// The vclock the snapshot ends at draws the line the fold stops
	// replaying behind: a journal overlapping the snapshot carries rows from
	// before it and after it both, and a row the checkpoint already holds is
	// history recovery would skip.
	meta, err := reader.ReadHeader(snapshot)
	if err != nil {
		return nil, fmt.Errorf("xlog: read _cluster: header %q: %w", snapshot, err)
	}

	seed, err := ReadCluster(snapshot)
	if err != nil {
		return nil, err
	}

	members := make(map[uint32]ClusterMember, len(seed))
	for _, member := range seed {
		members[member.ID] = member
	}

	for _, path := range journals {
		err := foldCluster(path, meta.VClock, upto, members)
		if err != nil {
			return nil, err
		}
	}

	list := make([]ClusterMember, 0, len(members))
	for _, member := range members {
		list = append(list, member)
	}

	slices.SortFunc(list, func(a, b ClusterMember) int { return cmp.Compare(a.ID, b.ID) })

	return list, nil
}

// foldCluster applies the _cluster rows of one journal to the membership,
// skipping the rows behind the snapshot's vclock and -- when upto is not
// nil -- the rows past it. A snapshot never carries deletes, so only a
// journal ever removes a member.
func foldCluster(
	path string,
	threshold, upto format.VClock,
	members map[uint32]ClusterMember,
) error {
	logReader, err := reader.Open(path)
	if err != nil {
		return fmt.Errorf("xlog: read _cluster: open %q: %w", path, err)
	}

	defer func() { _ = logReader.Close() }()

	for row, err := range logReader.Rows() {
		if err != nil {
			return fmt.Errorf("xlog: read _cluster: read %q: %w", path, err)
		}

		// A row at or behind the snapshot's component belongs to the state
		// the checkpoint already replaced -- Tarantool's recovery skips it,
		// and applying it here would resurrect a member as it was before.
		if lsn, covered := threshold[row.ReplicaID]; covered && row.LSN <= lsn {
			continue
		}

		// A row past the range's end belongs to what was written after the
		// freeze -- a journal a backup pinned keeps growing until the backup
		// is finalized -- and to a replica the freeze never saw at all: its
		// bound is 0, so every row it wrote lies past the range.
		if upto != nil {
			bound, seen := upto[row.ReplicaID]
			if !seen {
				bound = 0
			}

			if row.LSN > bound {
				continue
			}
		}

		var isClusterDML bool

		switch row.Type {
		case iproto.IPROTO_INSERT, iproto.IPROTO_REPLACE,
			iproto.IPROTO_DELETE, iproto.IPROTO_UPDATE:
			isClusterDML = true
		default:
			continue
		}

		body, err := format.DecodeDMLBody(row.BodyRaw)
		if err != nil || body.SpaceID != clusterSpaceID || !isClusterDML {
			continue
		}

		err = applyClusterRow(members, row.Type, body)
		if err != nil {
			return fmt.Errorf("xlog: read _cluster: row lsn %d in %q: %w",
				row.LSN, path, err)
		}
	}

	return nil
}

// applyClusterRow folds one decoded _cluster row into the membership: a
// replace or insert writes the member its tuple names, a delete forgets the
// member its key names, and an update names a member the replicaset had
// registered nameless.
func applyClusterRow(
	members map[uint32]ClusterMember,
	rowType iproto.Type,
	body *format.DMLBody,
) error {
	// Only membership-changing DML operations affect the cluster fold.
	//nolint:exhaustive // Other iproto operations carry no supported membership change.
	switch rowType {
	case iproto.IPROTO_INSERT, iproto.IPROTO_REPLACE:
		member, err := decodeClusterTuple(body.Tuple)
		if err != nil {
			return err
		}

		members[member.ID] = member
	case iproto.IPROTO_DELETE:
		id, err := decodeClusterKey(body.Key)
		if err != nil {
			return err
		}

		delete(members, id)
	case iproto.IPROTO_UPDATE:
		replicaID, err := decodeClusterKey(body.Key)
		if err != nil {
			return err
		}

		member, ok := members[replicaID]
		if !ok {
			return fmt.Errorf("%w %d the chain never registered",
				errClusterAnUpdateOfMember, replicaID)
		}

		err = applyClusterUpdate(&member, body.Ops, body.Tuple)
		if err != nil {
			return err
		}

		members[replicaID] = member
	}

	return nil
}

// applyClusterUpdate applies a _cluster update to the member. Tarantool
// names a member the replicaset registered nameless -- the one _cluster
// update it writes today -- and it packs the operations under the tuple
// key of the journal body, so the tuple bytes are the fallback when the
// dedicated operations key is empty. An operation this cannot apply is
// refused rather than guessed at: the fold's answer is what a restore
// verifies uuids by, and a silently skipped operation is a silently
// outdated membership.
func applyClusterUpdate(member *ClusterMember, ops, tuple []byte) error {
	if len(ops) == 0 {
		ops = tuple
	}

	dec := msgpack.NewDecoder(bytes.NewReader(ops))

	count, err := dec.DecodeArrayLen()
	if err != nil {
		return fmt.Errorf("decode operations: %w", err)
	}

	for index := range count {
		fields, err := dec.DecodeArrayLen()
		if err != nil {
			return fmt.Errorf("decode operation %d: %w", index, err)
		}

		opValue, err := dec.DecodeString()
		if err != nil {
			return fmt.Errorf("decode operation %d: %w", index, err)
		}

		field, err := dec.DecodeUint64()
		if err != nil {
			return fmt.Errorf("decode operation %d: %w", index, err)
		}

		// The name is the third field of a _cluster row; the one update
		// Tarantool writes sets exactly it.
		if opValue == "=" && fields == clusterUpdateFieldCount && field == clusterNameFieldNumber {
			member.Name, err = dec.DecodeString()
			if err != nil {
				return fmt.Errorf("decode operation %d: %w", index, err)
			}

			continue
		}

		return fmt.Errorf("%w %q on field %d no _cluster row carries",
			errClusterAnOperation, opValue, field)
	}

	return nil
}

// decodeClusterKey reads the single-field key of a _cluster delete.
func decodeClusterKey(key []byte) (uint32, error) {
	dec := msgpack.NewDecoder(bytes.NewReader(key))

	fields, err := dec.DecodeArrayLen()
	if err != nil {
		return 0, fmt.Errorf("decode key: %w", err)
	}

	if fields != 1 {
		return 0, fmt.Errorf("%w %d fields is not a _cluster key", errClusterAKeyOf, fields)
	}

	replicaID, err := dec.DecodeUint64()
	if err != nil {
		return 0, fmt.Errorf("decode member id: %w", err)
	}

	if replicaID > math.MaxUint32 {
		return 0, fmt.Errorf("%w %d is out of range", errClusterMemberID, replicaID)
	}

	return uint32(replicaID), nil
}

// decodeClusterTuple reads the [id, uuid, name?] tuple of a _cluster row. The
// name is absent for a member registered before the replicaset carried names
// and nil for one whose name was cleared; both leave Name empty. Fields past
// the name, none of which Tarantool writes today, are left unread.
func decodeClusterTuple(tuple []byte) (ClusterMember, error) {
	dec := msgpack.NewDecoder(bytes.NewReader(tuple))

	fields, err := dec.DecodeArrayLen()
	if err != nil {
		return ClusterMember{}, fmt.Errorf("decode tuple: %w", err)
	}

	if fields < clusterTupleMinFields {
		return ClusterMember{}, fmt.Errorf("%w %d fields is not a _cluster row",
			errClusterATupleOf, fields)
	}

	replicaID, err := dec.DecodeUint64()
	if err != nil {
		return ClusterMember{}, fmt.Errorf("decode member id: %w", err)
	}

	if replicaID > math.MaxUint32 {
		return ClusterMember{}, fmt.Errorf("%w %d is out of range", errClusterMemberID, replicaID)
	}

	memberUUID, err := dec.DecodeString()
	if err != nil {
		return ClusterMember{}, fmt.Errorf("decode member uuid: %w", err)
	}

	member := ClusterMember{ID: uint32(replicaID), UUID: memberUUID, Name: ""}

	if fields < clusterTupleNamedFields {
		return member, nil
	}

	code, err := dec.PeekCode()
	if err != nil {
		return ClusterMember{}, fmt.Errorf("decode member name: %w", err)
	}

	if code == msgpcode.Nil {
		return member, nil
	}

	member.Name, err = dec.DecodeString()
	if err != nil {
		return ClusterMember{}, fmt.Errorf("decode member name: %w", err)
	}

	return member, nil
}
