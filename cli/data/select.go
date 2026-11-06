package data

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"

	"github.com/tarantool/tt/sdk/log"
)

// Plan is the files a filter reads, in order, and the snapshot it starts
// from.
type Plan struct {
	// Files are the files to read: the snapshot, when there is one, and
	// the logs in the order of their vclocks.
	Files []*File
	// Base is the snapshot among Files, nil when there is none. The rows of
	// a write-ahead log it already contains are not read.
	Base *File
}

// Select plans what of files - in the order Collect returns them - filter
// reads, the way Tarantool recovers: from a snapshot and the write-ahead logs
// after it.
//
//   - At most one snapshot is read, the newest one within the upper bounds:
//     its vclock is at most --to or --to-vclock for each replica the bound
//     names, and it was made before --timestamp. The others are left out. A
//     lower bound leaves out every snapshot: it asks for the changes after a
//     point, and a snapshot holds a state. The snapshot is read first.
//   - A transaction the snapshot already contains, of a write-ahead log of
//     the instance that made it, is not read, and neither is a write-ahead
//     log that holds only such transactions or only transactions outside
//     the bounds.
//
// Select also settles whose LSN --from and --to bound: the replica Replicas
// names when it names one, or the one replica the headers of the files
// name. Headers that name several are ErrAmbiguousReplica; with none, the
// first row of a replica decides.
func Select(files []*File, filter *Filter) (*Plan, error) {
	err := filter.Check()
	if err != nil {
		return nil, err
	}

	err = settleReplica(files, filter)
	if err != nil {
		return nil, err
	}

	base, err := chooseSnapshot(files, filter)
	if err != nil {
		return nil, err
	}

	plan := &Plan{Files: nil, Base: base}
	if base != nil {
		plan.Files = append(plan.Files, base)
	}

	for i, file := range files {
		switch {
		case file.Kind == KindSnap:
			continue
		case file.Kind == KindXlog:
			next := nextXlog(files, i)
			if reason := skipXlog(file, next, base, filter); reason != "" {
				log.Debugf("Skipping %s: %s", file.Path, reason)

				continue
			}
		}

		plan.Files = append(plan.Files, file)
	}

	return plan, nil
}

// settleReplica sets the replica --from and --to bound.
func settleReplica(files []*File, filter *Filter) error {
	if !filter.lsnBounded() {
		return nil
	}

	if len(filter.Replicas) == 1 {
		filter.replica = filter.Replicas[0]

		return nil
	}

	named := map[uint32]bool{}

	for _, file := range files {
		if file.Kind == KindVylog {
			continue
		}

		for _, vclock := range []format.VClock{file.Meta.VClock, file.Meta.PrevVClock} {
			for id, lsn := range vclock {
				if id != 0 && lsn > 0 && (len(filter.Replicas) == 0 ||
					slices.Contains(filter.Replicas, id)) {
					named[id] = true
				}
			}
		}
	}

	ids := slices.Sorted(maps.Keys(named))

	switch len(ids) {
	case 0:
		return nil
	case 1:
		filter.replica = ids[0]

		return nil
	}

	return fmt.Errorf("%w, and the files have rows of replicas %v: name one with "+
		"--replica, or bound each with --from-vclock/--to-vclock", ErrAmbiguousReplica, ids)
}

// chooseSnapshot returns the snapshot the plan starts from: the newest one
// within the upper bounds, or nil.
func chooseSnapshot(files []*File, filter *Filter) (*File, error) {
	var base *File

	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		if file.Kind != KindSnap {
			continue
		}

		reason, err := snapshotOutOfBounds(file, filter)
		if err != nil {
			return nil, err
		}

		switch {
		case reason != "":
			log.Infof("Skipping the snapshot %s: %s", file.Path, reason)
		case base != nil:
			log.Infof("Skipping the snapshot %s: one snapshot is read, the newest, %s", file.Path,
				base.Path)
		default:
			base = file
		}
	}

	return base, nil
}

// snapshotOutOfBounds returns why the snapshot is outside the bounds of
// filter, or "" when it is within them.
func snapshotOutOfBounds(file *File, filter *Filter) (string, error) {
	vclock := file.Meta.VClock

	switch {
	case filter.lowerBounded():
		return "--from and --from-vclock select the changes after a point, " +
			"and a snapshot holds a state", nil
	case filter.HasTo && vclock[filter.replica] > filter.To:
		return fmt.Sprintf("it is at LSN %d of replica %d, past --to %d",
			vclock[filter.replica], filter.replica, filter.To), nil
	case filter.ToVClock != nil:
		for id, lsn := range filter.ToVClock {
			if vclock[id] > lsn {
				return fmt.Sprintf("its vclock %s is past --to-vclock %s", vclock,
					filter.ToVClock), nil
			}
		}
	}

	if filter.Before == 0 {
		return "", nil
	}

	made, err := snapshotTime(file.Path)
	if err != nil {
		return "", err
	}

	if made >= filter.Before {
		return fmt.Sprintf("it was made at %s, not before --timestamp", formatTime(made)), nil
	}

	return "", nil
}

// snapshotTime returns when a snapshot was made: the timestamp of its rows,
// which Tarantool sets to the time of the checkpoint. A snapshot with no rows
// is older than any time.
func snapshotTime(path string) (float64, error) {
	snap, err := reader.Open(path, reader.AcceptV012(), reader.IgnoreMissingEOF())
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	defer func() { _ = snap.Close() }()

	row, err := snap.Next()
	if errors.Is(err, io.EOF) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	return row.Timestamp, nil
}

// nextXlog returns the write-ahead log after files[i], or nil.
func nextXlog(files []*File, i int) *File {
	for _, file := range files[i+1:] {
		if file.Kind == KindXlog {
			return file
		}
	}

	return nil
}

// skipXlog returns why a write-ahead log holds nothing to read, judged by the
// vclocks its header and that of the next one give the transactions in it,
// or "" when it may hold something. Its rows are past its own vclock and at
// most at that of the next one.
func skipXlog(file, next, base *File, filter *Filter) string {
	start := file.Meta.VClock

	if next != nil {
		end := next.Meta.VClock

		if covers(base, file) && atMost(end, base.Meta.VClock) {
			return "the snapshot " + base.Path + " contains its transactions"
		}

		if filter.FromVClock != nil && atMost(end, filter.FromVClock) {
			return "its transactions are not past --from-vclock"
		}

		if filter.HasFrom && filter.replica != 0 && end[filter.replica] < filter.From {
			return "its transactions are before --from"
		}
	}

	if filter.HasTo && filter.replica != 0 && start[filter.replica] >= filter.To {
		return "its transactions are past --to"
	}

	if filter.ToVClock != nil && len(filter.ToVClock) > 0 {
		past := true

		for id, lsn := range filter.ToVClock {
			if start[id] < lsn {
				past = false
			}
		}

		if past {
			return "its transactions are past --to-vclock"
		}
	}

	return ""
}

// covers reports whether the snapshot base holds the changes of the
// transactions of file up to its vclock: file is a write-ahead log of the
// instance that made the snapshot. The vclock of a snapshot says nothing of
// the logs of another instance - of another replicaset, say - read along.
func covers(base, file *File) bool {
	return base != nil && file.Kind == KindXlog &&
		base.Meta.InstanceUUID == file.Meta.InstanceUUID
}

// atMost reports whether every component of a is at most that of b.
func atMost(a, b format.VClock) bool {
	for id, lsn := range a {
		if lsn > b[id] {
			return false
		}
	}

	return true
}
