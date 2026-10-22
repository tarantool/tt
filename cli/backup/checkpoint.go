package backup

import (
	"errors"
	"fmt"
	"time"

	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/connector"
)

var (
	errCheckpointTheInstanceReturnedNoCheckpointOutcome = errors.New(
		"the instance returned no checkpoint outcome",
	)
	errCheckpointUnexpectedCheckpointOutcome = errors.New("unexpected checkpoint outcome")
)

// snapshotExpr takes a new checkpoint on the instance, waiting out one already
// in progress. Its argument bounds the wait, in seconds.
//
// box.snapshot() during another checkpoint fails at once with
// CHECKPOINT_IN_PROGRESS, and box.backup.start() during one picks up the last
// completed checkpoint -- the very one a new checkpoint is taken to replace.
// So the wait is for the other checkpoint to complete. A completed one is
// returned as 'concurrent' and stands in for its own; one that failed leaves
// the newest checkpoint as it was, and a checkpoint of the instance's own is
// taken after all.
//
//nolint:dupword // Lua requires an end for each nested block.
const snapshotExpr = `
local timeout = ...
local clock = require('clock')
local fiber = require('fiber')

local function newest_signature()
    local checkpoints = box.info.gc().checkpoints
    local newest = checkpoints[#checkpoints]
    return newest and newest.signature
end

local deadline = clock.monotonic() + timeout
local before = newest_signature()
while true do
    local ok, err = pcall(box.snapshot)
    if ok then
        return 'taken'
    end
    if type(err) ~= 'cdata' or err.code ~= box.error.CHECKPOINT_IN_PROGRESS then
        error(err)
    end
    while box.info.gc().checkpoint_is_in_progress do
        if clock.monotonic() >= deadline then
            error(string.format('the checkpoint in progress on the instance ' ..
                'did not complete within %d seconds', timeout), 0)
        end
        fiber.sleep(0.1)
    end
    if newest_signature() ~= before then
        return 'concurrent'
    end
end
`

// checkpointWaitTimeout bounds the wait for a checkpoint already in progress
// on the instance to complete.
//

const checkpointWaitTimeout = time.Hour

// checkpointTaken and checkpointConcurrent are the two outcomes snapshotExpr
// reports: a checkpoint the box.snapshot() call took, and one a checkpoint
// already in progress completed just before.
const (
	checkpointTaken      = "taken"
	checkpointConcurrent = "concurrent"
)

// snapshotCheckpoint takes a new checkpoint with box.snapshot() for --snapshot,
// for the full backup about to open to ship, waiting out one already in
// progress. One completed by that checkpoint stands in for its own: it began
// just before. A failure stops the backup: nothing is open on the instance
// yet.
func snapshotCheckpoint(conn connector.Connector) error {
	res, err := conn.Eval(snapshotExpr, []any{checkpointWaitTimeout.Seconds()},
		connector.RequestOpts{PushCallback: nil, ReadTimeout: 0, ResData: nil})
	if err != nil {
		return fmt.Errorf("failed to take a checkpoint for the full backup: %w", err)
	}

	if len(res) == 0 {
		return errCheckpointTheInstanceReturnedNoCheckpointOutcome
	}

	switch res[0] {
	case checkpointTaken:
		return nil
	case checkpointConcurrent:
		log.Infof("a checkpoint was already in progress on the instance; " +
			"waited for it to complete")

		return nil
	default:
		return fmt.Errorf("%w %v", errCheckpointUnexpectedCheckpointOutcome, res[0])
	}
}
