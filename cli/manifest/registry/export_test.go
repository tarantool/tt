package registry

import (
	"io"
	"log/slog"
)

// HumanTo renders the result as Human does, noting a miss on logger.
func (r SearchResult) HumanTo(out io.Writer, logger *slog.Logger) error {
	return r.human(out, logger)
}
