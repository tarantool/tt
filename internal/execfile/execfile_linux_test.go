package execfile_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/execfile"
)

// TestWriteRunsWhileForking writes scripts and runs each at once while other
// goroutines start processes, as parallel tests do. Written with
// os.WriteFile, some 6% of the runs fail with "text file busy". Linux only:
// the kernel of macOS runs a file open for writing, so the test catches
// nothing there.
func TestWriteRunsWhileForking(t *testing.T) {
	t.Parallel()

	const runs = 300

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())

	var forkers sync.WaitGroup

	t.Cleanup(func() {
		cancel()
		forkers.Wait()
	})

	for range 4 {
		forkers.Go(func() {
			for ctx.Err() == nil {
				_ = exec.CommandContext(ctx, "true").Run()
			}
		})
	}

	for index := range runs {
		path := filepath.Join(dir, fmt.Sprintf("script-%d", index))

		require.NoError(t, execfile.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
		require.NoError(t, exec.CommandContext(t.Context(), path).Run(), "run %d", index)
	}
}
