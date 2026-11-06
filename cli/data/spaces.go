package data

import (
	"errors"
	"fmt"
	"io"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"

	"github.com/tarantool/tt/sdk/tuple"
)

// spaceSpaceID is the id of _space, the system space of space definitions.
const spaceSpaceID = 280

// Fields of a _space tuple: the id of the space and its name.
const (
	spaceFieldID   = 0
	spaceFieldName = 2
)

// SpaceIDs returns the ids of the spaces the snapshot of plan defines, by
// name, read from its _space rows. It is empty when the plan has no
// snapshot.
//
// A snapshot holds the spaces in the order of their ids, so the reading
// stops at the first row of a space past _space.
func SpaceIDs(plan *Plan) (map[string]uint32, error) {
	ids := map[string]uint32{}

	if plan.Base == nil {
		return ids, nil
	}

	snap, err := reader.Open(plan.Base.Path, reader.AcceptV012(), reader.IgnoreMissingEOF())
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", plan.Base.Path, err)
	}

	defer func() { _ = snap.Close() }()

	for {
		row, err := snap.Next()
		if errors.Is(err, io.EOF) || errors.Is(err, reader.ErrTruncated) {
			return ids, nil
		}

		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", plan.Base.Path, err)
		}

		if !isDML(row.Type) {
			continue
		}

		body, err := format.DecodeDMLBody(row.BodyRaw)
		if err != nil {
			continue
		}

		if body.SpaceID > spaceSpaceID {
			return ids, nil
		}

		if body.SpaceID != spaceSpaceID {
			continue
		}

		value, err := tuple.Decode(body.Tuple)
		if err != nil {
			continue
		}

		fields, ok := value.([]any)
		if !ok || len(fields) <= spaceFieldName {
			continue
		}

		id, isID := fields[spaceFieldID].(uint64)
		name, isName := fields[spaceFieldName].(string)

		if isID && isName {
			ids[name] = uint32(id) //nolint:gosec // A space id fits 32 bits.
		}
	}
}
