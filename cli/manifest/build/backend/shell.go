package backend

import (
	"context"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// shellBackend runs an arbitrary command argv-style: execve of build.Command
// with build.Args, no shell parsing (a pipeline needs its own wrapper script).
// It is the widest backend, for everything that is neither cc nor make.
type shellBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// Run executes build.Command with build.Args in cwd under the env contract. A
// non-zero exit is a build error. On success, if build.Output is set the listed
// files are copied into env.OutputDir; otherwise the command was expected to
// write there itself.
func (s shellBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	err = run(ctx, cwd, env, s.showOutput, build.Command, build.Args...)
	if err != nil {
		return err
	}

	if len(build.Output) == 0 {
		return nil
	}

	return copyOutputs(env.OutputDir, cwd, build.Output)
}
