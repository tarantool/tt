package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
)

// firstUserSpaceID is the id of the first space that is not a system one.
const firstUserSpaceID = 512

var (
	// ErrBoundsConflict is --from or --to given together with --from-vclock or
	// --to-vclock.
	ErrBoundsConflict = errors.New(
		"--from/--to and --from-vclock/--to-vclock are mutually exclusive")
	// ErrEmptyRange is a lower bound past the upper one.
	ErrEmptyRange = errors.New("the lower bound is past the upper one")
	// ErrAmbiguousReplica is --from or --to on files with rows of several
	// replicas and no --replica to say whose LSN they bound.
	ErrAmbiguousReplica = errors.New("--from/--to bound the LSN of one replica")
	// ErrInvalidVClock is a vclock flag that does not parse.
	ErrInvalidVClock = errors.New("invalid vclock")
)

// Filter is what of the journals to read: the bounds of the range, the time
// to stop at, and the rows to keep of the transactions within them.
//
// The bounds select whole transactions: a transaction is in the range when
// any of its rows is, so a bound that falls inside a transaction takes all
// of it. They apply to write-ahead logs only: the rows of a snapshot have
// row numbers, not LSNs, and a snapshot is chosen whole (see [Select]).
type Filter struct {
	// From and To, when HasFrom and HasTo are set, bound the LSN of one
	// replica, both ends included: a transaction is in the range when it
	// has a row of that replica with an LSN from From to To. The replica is
	// the one Replicas names when it names one, or the only one the files
	// have rows of.
	From, To       int64
	HasFrom, HasTo bool
	// FromVClock, when not nil, takes the transactions past it: those with a
	// row whose LSN is above the LSN FromVClock has for its replica.
	FromVClock format.VClock
	// ToVClock, when not nil, takes the transactions within it: those with a
	// row whose LSN is at most the LSN ToVClock has for its replica. A
	// replica a vclock does not name has LSN 0 in it.
	ToVClock format.VClock
	// Before, when not zero, takes the transactions written before it, in
	// seconds since the epoch.
	Before float64
	// Replicas, when not empty, keeps the rows these replicas wrote.
	Replicas []uint32
	// Spaces, when not empty, keeps the rows of these spaces, system ones
	// included, and no row that is not a data change.
	Spaces []uint32
	// ShowSystem keeps the rows of system spaces and the rows that are not
	// data changes: RAFT, synchronous replication, NOP. Without it only
	// changes of user spaces are kept.
	ShowSystem bool

	// replica is the replica From and To bound, 0 until it is known.
	replica uint32
}

// Check reports a filter whose bounds contradict each other.
func (f *Filter) Check() error {
	if (f.HasFrom || f.HasTo) && (f.FromVClock != nil || f.ToVClock != nil) {
		return ErrBoundsConflict
	}

	if f.HasFrom && f.HasTo && f.From > f.To {
		return fmt.Errorf("%w: --from %d is past --to %d", ErrEmptyRange, f.From, f.To)
	}

	if f.FromVClock != nil && f.ToVClock != nil {
		for id, lsn := range f.FromVClock {
			if lsn >= f.ToVClock[id] {
				return fmt.Errorf("%w: --from-vclock %s is not below --to-vclock %s",
					ErrEmptyRange, f.FromVClock, f.ToVClock)
			}
		}
	}

	return nil
}

// lsnBounded reports whether From or To is set.
func (f *Filter) lsnBounded() bool {
	return f.HasFrom || f.HasTo
}

// lowerBounded reports whether the range has a lower bound.
func (f *Filter) lowerBounded() bool {
	return f.HasFrom || f.FromVClock != nil
}

// bounded reports whether the range has any bound.
func (f *Filter) bounded() bool {
	return f.lsnBounded() || f.FromVClock != nil || f.ToVClock != nil
}

// inRange reports whether row is within the bounds.
func (f *Filter) inRange(row format.XRow) bool {
	if f.lsnBounded() {
		return row.ReplicaID == f.replica &&
			(!f.HasFrom || row.LSN >= f.From) && (!f.HasTo || row.LSN <= f.To)
	}

	if f.FromVClock != nil && row.LSN <= f.FromVClock[row.ReplicaID] {
		return false
	}

	if f.ToVClock != nil && row.LSN > f.ToVClock[row.ReplicaID] {
		return false
	}

	return true
}

// bindReplica makes the replica of row the one From and To bound, if it is
// not known yet, and reports a row of another replica. Rows of replica 0,
// the changes of local spaces, have LSNs of their own and bind nothing.
func (f *Filter) bindReplica(row format.XRow, file string) error {
	if !f.lsnBounded() || row.ReplicaID == 0 || len(f.Replicas) == 1 {
		return nil
	}

	switch f.replica {
	case 0:
		f.replica = row.ReplicaID
	case row.ReplicaID:
	default:
		return fmt.Errorf("%w, and %s has rows of replicas %d and %d: name one with "+
			"--replica, or bound each with --from-vclock/--to-vclock",
			ErrAmbiguousReplica, file, f.replica, row.ReplicaID)
	}

	return nil
}

// isDML reports whether t is a data change of a space.
func isDML(t iproto.Type) bool {
	switch t {
	case iproto.IPROTO_INSERT, iproto.IPROTO_REPLACE, iproto.IPROTO_UPDATE,
		iproto.IPROTO_DELETE, iproto.IPROTO_UPSERT, iproto.IPROTO_INSERT_ARROW:
		return true
	}

	return false
}

// keepRow reports whether the row filters keep row, a row of a file of
// kind: the replica, the space and the system filters. The rows of a vinyl
// log are all kept: they are metadata, not data changes. A replica does not
// write the rows of a snapshot, so Replicas does not apply to them.
func (f *Filter) keepRow(kind Kind, row *Row) bool {
	if kind == KindVylog {
		return true
	}

	if kind != KindSnap && len(f.Replicas) > 0 && !slices.Contains(f.Replicas, row.ReplicaID) {
		return false
	}

	if row.DML == nil {
		return len(f.Spaces) == 0 && f.ShowSystem
	}

	if len(f.Spaces) > 0 {
		return slices.Contains(f.Spaces, row.DML.SpaceID)
	}

	return f.ShowSystem || row.DML.SpaceID >= firstUserSpaceID
}

// ParseVClock parses a vclock given on the command line: the form Tarantool
// prints, {1: 100, 2: 50}, or a JSON object, {"1": 100, "2": 50}.
func ParseVClock(value string) (format.VClock, error) {
	var object map[string]int64

	err := json.Unmarshal([]byte(value), &object)
	if err != nil {
		vclock, parseErr := format.ParseVClock(value)
		if parseErr != nil {
			return nil, fmt.Errorf("%w %q: want {1: 100, 2: 50} or {\"1\": 100, \"2\": 50}",
				ErrInvalidVClock, value)
		}

		object = make(map[string]int64, len(vclock))
		for id, lsn := range vclock {
			object[strconv.FormatUint(uint64(id), 10)] = lsn
		}
	}

	if object == nil {
		return nil, fmt.Errorf("%w %q: want an object", ErrInvalidVClock, value)
	}

	vclock := make(format.VClock, len(object))

	for key, lsn := range object {
		id, err := strconv.ParseUint(strings.TrimSpace(key), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w %q: replica id %q is not a number", ErrInvalidVClock,
				value, key)
		}

		if lsn < 0 {
			return nil, fmt.Errorf("%w %q: LSN %d of replica %d is negative", ErrInvalidVClock,
				value, lsn, id)
		}

		vclock[uint32(id)] = lsn
	}

	return vclock, nil
}
