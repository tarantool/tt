package build

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// This file checks Options.Jobs: the Jobs of the Context of every build and
// hook, TT_BUILD_JOBS of their commands, and that it is no input of an
// incremental build.

// TestRun_jobsInTheContext checks that the Context of the build of a
// component has the Jobs of the options, and its Env TT_BUILD_JOBS, the
// number of CPUs when Jobs is 0.
func TestRun_jobsInTheContext(t *testing.T) {
	t.Parallel()

	for _, jobs := range []int{0, 3} {
		t.Run(strconv.Itoa(jobs), func(t *testing.T) {
			t.Parallel()

			dir := setupProject(t, touchManifest, map[string]string{
				"fast/init.lua": "return {}\n",
				"tool/main.txt": "the sources of the tool\n",
			})

			recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

			backends := buildregtest.Registry()
			require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)},
				nil))

			opts := dryOptions(dir)

			opts.Backends = backends
			opts.Jobs = jobs

			require.NoError(t, Run(context.Background(), opts))

			want := jobs
			if want == 0 {
				want = runtime.NumCPU()
			}

			for _, name := range []string{"fast", "tool"} {
				facts, ok := recorder.ran(name)
				require.True(t, ok, name)
				assert.Equal(t, jobs, facts.Jobs, name)
				assert.Equal(t, want, facts.JobsOrCPUs(), name)
				assert.Equal(t, strconv.Itoa(want), facts.Env["TT_BUILD_JOBS"], name)
			}
		})
	}
}

// TestRun_jobsIsNoIncrementalInput checks that a build with other Jobs is
// skipped as one with the same: the record of an incremental component, the
// hash of its inputs, stays as it was, and a component that is built every
// time is not built from scratch either, as one whose block changed is.
func TestRun_jobsIsNoIncrementalInput(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, touchManifest, map[string]string{
		"fast/init.lua": "return {}\n",
		"tool/main.txt": "the sources of the tool\n",
	})

	recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, 1, runs(t, dir, "fast"))

	state := filepath.Join(dir, buildDirName)

	record, found := readIncremental(state, "fast")
	require.True(t, found)

	for _, jobs := range []int{1, 7, runtime.NumCPU(), 0} {
		other := opts

		other.Jobs = jobs

		require.NoError(t, Run(context.Background(), other))
		assert.Equal(t, 1, runs(t, dir, "fast"), "jobs %d: the build is skipped", jobs)

		again, found := readIncremental(state, "fast")
		require.True(t, found)
		assert.Equal(t, record, again, "jobs %d: the record is the same", jobs)

		tool, ok := recorder.ran("tool")
		require.True(t, ok)
		assert.Equal(t, jobs, tool.Jobs)
		assert.False(t, tool.Fresh, "jobs %d: no build from scratch", jobs)
	}

	// The inputs the core hashes are the same whatever the jobs.
	man, err := readManifest(dir, opts)
	require.NoError(t, err)

	proj := packageProject(dir, man, "default")
	stage := packageStage(dir, "default")

	hashes := map[string]bool{}

	for _, jobs := range []int{0, 1, 5} {
		other := opts

		other.Jobs = jobs

		builder := newBuilder(other, proj, stage, version.Version{})

		run, err := builder.component("fast")
		require.NoError(t, err)

		inputs, err := builder.inputs(context.Background(), run)
		require.NoError(t, err)

		hashes[inputs] = true
	}

	assert.Len(t, hashes, 1)
	assert.True(t, hashes[record.Inputs], "the hash of the record")
}

// TestRun_jobsOfAHook checks that a hook is told the jobs as a build is: in
// the Jobs of its Context and TT_BUILD_JOBS of its commands, a shell hook's
// as a hook of a module's backend.
func TestRun_jobsOfAHook(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[hooks.pre_build]
backend = 'hooker'

[hooks.post_build]
command = 'sh'
args = ['-c', 'printf %s "${TT_BUILD_JOBS-unset}" > hook.txt']
`, nil)

	recorder := &hookRecorder{mu: sync.Mutex{}, contexts: nil, printed: nil}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("hooks", []sdkbuild.Backend{hookBackend(recorder)}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends
	opts.Jobs = 5

	require.NoError(t, Run(context.Background(), opts))

	require.Len(t, recorder.contexts, 1)
	assert.Equal(t, 5, recorder.contexts[0].Jobs)
	assert.Equal(t, "5", recorder.contexts[0].Env["TT_BUILD_JOBS"])

	data, err := os.ReadFile(filepath.Join(dir, "hook.txt"))
	require.NoError(t, err)
	assert.Equal(t, "5", string(data))

	// With no jobs, the hook is told the number of CPUs.
	opts.Jobs = 0

	require.NoError(t, Run(context.Background(), opts))

	data, err = os.ReadFile(filepath.Join(dir, "hook.txt"))
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(runtime.NumCPU()), string(data))
}

// TestRun_jobsOfAShellBuild checks that the command of a shell component
// gets TT_BUILD_JOBS, which a make -j"$TT_BUILD_JOBS" of a script reads, and
// that an incremental one is not built again for other jobs.
func TestRun_jobsOfAShellBuild(t *testing.T) {
	t.Parallel()

	dir := setupProject(t,
		incrementalShell(`printf %s "$TT_BUILD_JOBS" > "$TT_OUTPUT_DIR/jobs.txt"`, ""),
		map[string]string{"comp/init.lua": "return {}"})

	opts := dryOptions(dir)

	opts.Jobs = 2

	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, 1, countRuns(t, dir))

	jobsFile := filepath.Join(packageStage(dir, "default"), "lib", "my-app", "jobs.txt")

	data, err := os.ReadFile(jobsFile)
	require.NoError(t, err)
	assert.Equal(t, "2", string(data))

	opts.Jobs = 6

	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, 1, countRuns(t, dir), "other jobs: the build is skipped")

	// A rebuild is told the jobs of its own run.
	opts.Rebuild = true

	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, 2, countRuns(t, dir))

	data, err = os.ReadFile(jobsFile)
	require.NoError(t, err)
	assert.Equal(t, "6", string(data))
}
