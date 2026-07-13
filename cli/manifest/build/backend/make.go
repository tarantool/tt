package backend

import (
	"context"

	"github.com/tarantool/tt/cli/manifest"
)

// defaultMakefile is the entrypoint used when b.Entrypoint is empty.
const defaultMakefile = "Makefile"

// makeBackend drives make against a component-supplied Makefile. tt never
// parses the Makefile; the target sees TT_OUTPUT_DIR and the rest of the
// contract through the environment.
type makeBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// makeArgs builds the make argv: make -C <cwd> -f <entrypoint> <make_target>
// followed by b.Flags (e.g. -j4). entrypoint defaults to Makefile in cwd.
func makeArgs(b manifest.Build, cwd string) []string {
	entrypoint := b.Entrypoint
	if entrypoint == "" {
		entrypoint = defaultMakefile
	}

	args := []string{"-C", cwd, "-f", entrypoint, b.MakeTarget}

	return append(args, b.Flags...)
}

// Run invokes make in cwd under the env contract. A non-zero exit is a build
// error. On success, if b.Output is set the listed files are copied into
// env.OutputDir (a make target usually writes into TT_OUTPUT_DIR itself, but
// the copy is honored uniformly).
func (m makeBackend) Run(ctx context.Context, b manifest.Build, cwd string, env Env) error {
	if err := requireAbsPaths(cwd, env.OutputDir); err != nil {
		return err
	}

	err := run(ctx, cwd, env, m.showOutput, "make", makeArgs(b, cwd)...)
	if err != nil {
		return err
	}

	if len(b.Output) == 0 {
		return nil
	}

	return copyOutputs(env.OutputDir, cwd, b.Output)
}
