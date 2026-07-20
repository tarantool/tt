package pack

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// sourceDateEpochEnv is the cross-ecosystem convention for pinning a build
// timestamp; honouring it lets a CI job make tt agree with everything else it
// builds.
const sourceDateEpochEnv = "SOURCE_DATE_EPOCH"

// buildTime picks the timestamp stamped into the generated version.lua, and
// with it whether the archive is reproducible at all.
//
// The wall clock would make every pack of the same commit produce a different
// archive, which defeats the reproducible-archive guarantee the checksum rests
// on. So the clock is pinned, in order: SOURCE_DATE_EPOCH when set, then the
// HEAD commit's own timestamp, and only failing both the wall clock — the last
// case being a tree with no git history, where reproducibility is unreachable
// anyway.
func buildTime(ctx context.Context, projectDir string) time.Time {
	if epoch, ok := sourceDateEpoch(); ok {
		return epoch
	}

	if commit, ok := commitTime(ctx, projectDir); ok {
		return commit
	}

	return time.Now()
}

// sourceDateEpoch reads SOURCE_DATE_EPOCH as Unix seconds.
func sourceDateEpoch() (time.Time, bool) {
	raw := strings.TrimSpace(os.Getenv(sourceDateEpochEnv))
	if raw == "" {
		return time.Time{}, false
	}

	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// A malformed value is ignored rather than fatal: it is an ambient
		// environment variable, not something this pack invocation asked for.
		return time.Time{}, false
	}

	return time.Unix(secs, 0).UTC(), true
}

// commitTime returns the HEAD commit's author timestamp.
func commitTime(ctx context.Context, projectDir string) (time.Time, bool) {
	//nolint:gosec // Fixed program (git) with internally built args.
	cmd := exec.CommandContext(ctx,
		"git", "-C", projectDir, "log", "-1", "--format=%at")

	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}

	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	return time.Unix(secs, 0).UTC(), true
}
