package coredump

import "log/slog"

// hintPrefix begins the line that says what to do of what a warning says.
const hintPrefix = "hint: "

// warnWithHint logs warning, what will not work and what it breaks, and
// then hint, what to do of it, as two warnings: each is a short line, and
// the hint is not lost in the middle of a long one.
func warnWithHint(log *slog.Logger, warning, hint string) {
	log.Warn(warning)
	log.Warn(hintPrefix + hint)
}
