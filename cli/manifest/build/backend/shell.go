package backend

import (
	"context"

	"github.com/tarantool/tt/cli/manifest"
)

// shellBackend runs an arbitrary command argv-style: execve of b.Command with
// b.Args, no shell parsing (a pipeline needs its own wrapper script). It is the
// widest backend, for everything that is neither cc nor make.
type shellBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// Run executes b.Command with b.Args in cwd under the env contract. A non-zero
// exit is a build error. On success, if b.Output is set the listed files are
// copied into env.OutputDir; otherwise the command was expected to write there
// itself.
func (s shellBackend) Run(ctx context.Context, b manifest.Build, cwd string, env Env) error {
	if err := requireAbsPaths(cwd, env.OutputDir); err != nil {
		return err
	}

	err := run(ctx, cwd, env, s.showOutput, b.Command, b.Args...)
	if err != nil {
		return err
	}

	if len(b.Output) == 0 {
		return nil
	}

	return copyOutputs(env.OutputDir, cwd, b.Output)
}
