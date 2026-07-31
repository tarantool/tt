package restore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// Point is a recovery point position: the LSN of one replica's row in the
// journal. It is the shape `tt restore plan` prints under
// recovery_point.trim_to_by_replicaset[<replicaset_uuid>].
type Point struct {
	ReplicaID uint32 `json:"replica_id"`
	LSN       uint64 `json:"lsn"`
}

// String renders the point the way it is written on the command line.
func (p Point) String() string {
	return fmt.Sprintf("{replica_id: %d, lsn: %d}", p.ReplicaID, p.LSN)
}

// ParsePoint decodes the --target-point argument.
//
// Unknown keys are rejected rather than ignored: a misspelled "replica_id"
// would otherwise decode to 0 and silently trim on the wrong axis, and
// picking the wrong axis is exactly the failure this command has to avoid.
func ParsePoint(raw string) (*Point, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()

	var point Point
	if err := dec.Decode(&point); err != nil {
		return nil, fmt.Errorf(
			"%w: --target-point %q is not {\"replica_id\":N,\"lsn\":M}: %w",
			ErrValidation, raw, err)
	}

	// Replica 0 is Tarantool's marker for rows that belong to no replica; a
	// recovery point never sits on it, so a zero here means the field was
	// missing rather than deliberately set.
	if point.ReplicaID == 0 {
		return nil, fmt.Errorf("%w: --target-point %q has no replica_id",
			ErrValidation, raw)
	}

	if point.LSN == 0 {
		return nil, fmt.Errorf("%w: --target-point %q has no lsn", ErrValidation, raw)
	}

	if point.LSN > math.MaxInt64 {
		return nil, fmt.Errorf("%w: --target-point %q lsn %d exceeds int64",
			ErrValidation, raw, point.LSN)
	}

	return &point, nil
}
