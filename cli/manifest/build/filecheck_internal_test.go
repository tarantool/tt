package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3"
	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// errBadFile is what the check of the tests says of a file it refuses.
var errBadFile = errors.New("a bad file")

// fileChecker is a file check of a module that records the files it was
// given and says what their content asks: a warning for "warn", an error for
// "bad", nothing else.
type fileChecker struct {
	mu      sync.Mutex
	checked []string
}

// check is the Check of the file check.
func (c *fileChecker) check(_ context.Context, path string) (string, error) {
	c.mu.Lock()

	c.checked = append(c.checked, path)

	c.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	switch string(data) {
	case "warn":
		return path + " is to be warned of", nil
	case "bad":
		return "", fmt.Errorf("%s: %w", path, errBadFile)
	default:
		return "", nil
	}
}

// files returns the files checked so far and forgets them.
func (c *fileChecker) files() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	files := c.checked

	c.checked = nil

	return files
}

// TestRun_fileChecksOfTheModules checks that the file checks of the modules
// are given every file of the package with their suffix, the files the
// layout of a component copies and the data files a build makes in its share
// directory, those of an incremental build skipped included, and none of the
// modules and programs; that their warnings are shown; and that an error of
// a check fails the build with exit code 1, its text as it is.
func TestRun_fileChecksOfTheModules(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, touchManifest+`
[components.data]
path = 'data'
include = ['*.chk']
`, map[string]string{
		"fast/init.lua": "return {}\n",
		"tool/main.txt": "the sources of the tool\n",
		"data/laid.chk": "warn",
	})

	checker := &fileChecker{mu: sync.Mutex{}, checked: nil}
	recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)}, nil))
	require.NoError(t, backends.Add("check", nil, []sdkbuild.FileCheck{
		{Suffix: ".chk", Check: checker.check},
		// The data file touch makes, which the checks are given as the
		// files laid out are.
		{Suffix: ".wasm", Check: checker.check},
	}))

	var warnings []string

	opts := dryOptions(dir)

	opts.Backends = backends
	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	stage := packageStage(dir, "default")
	laid := filepath.Join(stage, "lua", "my-app", "laid.chk")
	made := filepath.Join(stage, "lua", "my-app", "fast.wasm")

	for build := 1; build <= 2; build++ {
		warnings = nil

		_, err := RunResult(context.Background(), opts)
		require.NoError(t, err, build)

		assert.ElementsMatch(t, []string{made, laid}, checker.files(), build)
		assert.Contains(t, warnings, laid+" is to be warned of", build)
	}

	assert.Equal(t, 1, runs(t, dir, "fast"), "the second build of fast was skipped")

	// A file a check refuses fails the build, with what the check says.
	writeFile(t, filepath.Join(dir, "data", "laid.chk"), "bad")

	warnings = nil

	_, err := RunResult(context.Background(), opts)
	require.ErrorIs(t, err, errBadFile)
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
	require.EqualError(t, err, laid+": "+errBadFile.Error())
	assert.NotContains(t, warnings, laid+" is to be warned of")
}

// buildKey is the key of a value of the context of a build.
type buildKey struct{}

// TestCheckFiles checks the order the checks are run in, the files that are
// given to none, and the first error, which drops the warnings; and that a
// check is given the context of the build.
func TestCheckFiles(t *testing.T) {
	t.Parallel()

	var calls []string

	ctx := context.WithValue(t.Context(), buildKey{}, "the build")

	checkOf := func(name string, fail bool) sdkbuild.FileCheck {
		return sdkbuild.FileCheck{Suffix: ".x", Check: func(ctx context.Context, path string) (
			string, error,
		) {
			assert.Equal(t, "the build", ctx.Value(buildKey{}))

			calls = append(calls, name+" "+path)

			if fail && path == "b.x" {
				return "", errBadFile
			}

			return name + " warns of " + path, nil
		}}
	}

	warnings, err := checkFiles(ctx, []sdkbuild.FileCheck{checkOf("one", false),
		checkOf("two", false)}, []string{"a.x", "a.y", "b.x"})
	require.NoError(t, err)
	assert.Equal(t, []string{"one a.x", "two a.x", "one b.x", "two b.x"}, calls)
	assert.Equal(t, []string{
		"one warns of a.x", "two warns of a.x", "one warns of b.x", "two warns of b.x",
	}, warnings)

	calls = nil

	warnings, err = checkFiles(ctx, []sdkbuild.FileCheck{checkOf("one", true),
		checkOf("two", false)}, []string{"a.x", "b.x", "c.x"})
	require.ErrorIs(t, err, errBadFile)
	assert.Nil(t, warnings)
	assert.Equal(t, []string{"one a.x", "two a.x", "one b.x"}, calls)

	warnings, err = checkFiles(ctx, nil, []string{"a.x"})
	require.NoError(t, err)
	assert.Nil(t, warnings)
}
