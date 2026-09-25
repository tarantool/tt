package backend

import (
	"context"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// defaultMakefile is the entrypoint used when build.Entrypoint is empty.
const defaultMakefile = "Makefile"

// makeBackend drives make against a component-supplied Makefile. tt never
// parses the Makefile; the target sees TT_OUTPUT_DIR and the rest of the
// contract through the environment.
type makeBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// makeArgs builds the make argv: make -C <cwd> -f <entrypoint> <make_target>
// followed by build.Flags (e.g. -j4). entrypoint defaults to Makefile in cwd.
func makeArgs(build manifest.Build, cwd string) []string {
	entrypoint := build.Entrypoint
	if entrypoint == "" {
		entrypoint = defaultMakefile
	}

	return append([]string{"-C", cwd, "-f", entrypoint, build.MakeTarget}, build.Flags...)
}

// Run invokes make in cwd under the env contract. A non-zero exit is a build
// error. On success, if build.Output is set the listed files are copied into
// env.OutputDir (a make target usually writes into TT_OUTPUT_DIR itself, but
// the copy is honored uniformly).
func (m makeBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	err = run(ctx, cwd, env, m.showOutput, "make", makeArgs(build, cwd)...)
	if err != nil {
		return err
	}

	if len(build.Output) == 0 {
		return nil
	}

	return copyOutputs(env.OutputDir, cwd, build.Output)
}
