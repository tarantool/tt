package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/logging"
	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// restoreProcess puts back, when the test ends, the process state a test of
// the core replaces: the CmdCtx and the process logger.
func restoreProcess(t *testing.T) {
	t.Helper()

	ctx := cmd.GetCmdCtxPtr()
	previousCtx, previousLogger := *ctx, slog.Default()

	t.Cleanup(func() {
		*ctx = previousCtx

		slog.SetDefault(previousLogger)
	})
}

// readyServices returns the Services of the module "m", ready, reading stdin
// from input and writing to out and errOut.
func readyServices(input io.Reader, out, errOut io.Writer) *services {
	ready := &atomic.Bool{}
	ready.Store(true)

	svc := newServices("m", ready)

	svc.streams = output.Streams{In: input, Out: out, Err: errOut}

	return svc
}

// TestServicesReadiness checks that only Log works before tt is configured.
func TestServicesReadiness(t *testing.T) {
	t.Parallel()

	ready := &atomic.Bool{}
	svc := newServices("early", ready)

	assert.NotNil(t, svc.Log())

	for method, call := range map[string]func(){
		"Tarantool": func() { _, _ = svc.Tarantool() },
		"Integrity": func() { svc.Integrity() },
		"Project":   func() { svc.Project() },
		"Confirm":   func() { _, _ = svc.Confirm("?", true) },
		"Streams":   func() { svc.Streams() },
		"ClusterConfig": func() {
			_, _ = svc.ClusterConfig(t.Context(), sdk.FileSource("config.yaml"))
		},
		"Exit": func() { svc.Exit(nil) },
	} {
		assert.PanicsWithValue(t, `module "early" used Services.`+method+
			` before tt was configured; use it in a command's hooks, not in the constructor`,
			call, method)
	}

	ready.Store(true)
	assert.NotPanics(t, func() { svc.Streams() })
}

// TestServicesLog checks that a module's logger is the process logger with
// the module's name, spinner included.
//
//nolint:paralleltest // Replaces the process logger.
func TestServicesLog(t *testing.T) {
	restoreProcess(t)

	var written bytes.Buffer

	require.NoError(t, logging.Setup(logging.Options{
		Level:      slog.LevelInfo,
		Format:     logging.FormatText,
		Writer:     &written,
		Redactor:   nil,
		IsTerminal: func(io.Writer) bool { return true },
		LookupEnv: func(key string) (string, bool) {
			switch key {
			case "TERM":
				return "xterm", true
			case "NO_COLOR":
				return "1", true
			default:
				return "", false
			}
		},
	}))

	svc := newServices("demo", &atomic.Bool{})

	stop := log.SpinnerOf(svc.Log(), "spinning")
	stop()
	assert.Contains(t, written.String(), "spinning", "the spinner draws on the terminal")

	written.Reset()
	svc.Log().Info("hello")
	assert.Contains(t, written.String(), "hello")
	assert.Contains(t, written.String(), "module=demo")
}

// writeTarantool writes a fake tarantool at path whose --version prints
// banner.
func writeTarantool(t *testing.T, path, banner string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	script := "#!/bin/sh\nprintf '" + banner + "\\nTarget: test\\n'\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

// TestServicesTarantool checks that Tarantool prefers the project's bundled
// runtime, falls back to the environment's and reports its version and
// edition.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestServicesTarantool(t *testing.T) {
	restoreProcess(t)

	project := t.TempDir()
	t.Chdir(project)
	t.Setenv(run.UseSystemEnv, "")
	t.Setenv("PATH", t.TempDir())

	environment := filepath.Join(t.TempDir(), "tarantool")
	writeTarantool(t, environment, "Tarantool 3.9.0-0-gabcdef1")

	bundled := filepath.Join(project, "_runtime", "tarantool", "bin", "tarantool")
	writeTarantool(t, bundled, "Tarantool Enterprise 3.2.1-12-g1a2b3c4-r579")

	cmd.GetCmdCtxPtr().Cli.TarantoolCli.Executable = environment

	svc := readyServices(strings.NewReader(""), io.Discard, io.Discard)

	check := func(wantPath string, want sdk.TarantoolVersion) {
		t.Helper()

		got, err := svc.Tarantool()
		require.NoError(t, err)

		resolved, err := filepath.EvalSymlinks(got.Path())
		require.NoError(t, err)

		wantResolved, err := filepath.EvalSymlinks(wantPath)
		require.NoError(t, err)
		assert.Equal(t, wantResolved, resolved)

		version, err := got.Version()
		require.NoError(t, err)
		assert.Equal(t, want, version)
	}

	bundledVersion := sdk.TarantoolVersion{
		Major: 3, Minor: 2, Patch: 1, Commits: 12, Hash: "1a2b3c4", Revision: 579,
		Edition: sdk.EditionEE, Raw: "3.2.1-12-g1a2b3c4-r579",
	}
	environmentVersion := sdk.TarantoolVersion{
		Major: 3, Minor: 9, Patch: 0, Hash: "abcdef1",
		Edition: sdk.EditionCE, Raw: "3.9.0-0-gabcdef1",
	}

	check(bundled, bundledVersion)

	t.Setenv(run.UseSystemEnv, "1")
	check(environment, environmentVersion)
	t.Setenv(run.UseSystemEnv, "")

	require.NoError(t, os.RemoveAll(filepath.Join(project, "_runtime")))
	check(environment, environmentVersion)

	cmd.GetCmdCtxPtr().Cli.TarantoolCli.Executable = ""

	_, err := svc.Tarantool()
	require.ErrorIs(t, err, sdk.ErrNotFound)
	require.ErrorIs(t, err, run.ErrNoTarantool)
}

// TestServicesConfirm checks the prompt, the answers it takes and
// --no-prompt.
//
//nolint:paralleltest // Changes the CmdCtx.
func TestServicesConfirm(t *testing.T) {
	restoreProcess(t)

	var prompts bytes.Buffer

	svc := readyServices(strings.NewReader("maybe\nYES\nn\nrest"), io.Discard, &prompts)

	answer, err := svc.Confirm("Go on?", false)
	require.NoError(t, err)
	assert.True(t, answer)
	assert.Equal(t, "Go on? [y/n]: Go on? [y/n]: ", prompts.String())

	answer, err = svc.Confirm("Again?", true)
	require.NoError(t, err)
	assert.False(t, answer)

	rest, err := io.ReadAll(svc.streams.In)
	require.NoError(t, err)
	assert.Equal(t, "rest", string(rest), "nothing past the answer is read")

	_, err = svc.Confirm("At the end?", true)
	require.ErrorIs(t, err, io.EOF)

	cmd.GetCmdCtxPtr().Cli.NoPrompt = true

	prompts.Reset()

	for _, fallback := range []bool{true, false} {
		answer, err = svc.Confirm("Skipped?", fallback)
		require.NoError(t, err)
		assert.Equal(t, fallback, answer)
	}

	assert.Empty(t, prompts.String())
}

// printedResult is what the printer of TestServicesStreams emits.
type printedResult struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Human renders the result for people.
func (r printedResult) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s: %d\n", r.Name, r.Count)

	return err
}

// TestServicesStreams checks that the printers have the core's formats.
func TestServicesStreams(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	svc := readyServices(strings.NewReader(""), &out, io.Discard)

	printer, err := svc.Streams().Printer(output.FormatYAML)
	require.NoError(t, err)
	require.NoError(t, printer.Emit(printedResult{Name: "x", Count: 1}))
	assert.Equal(t, "name: x\ncount: 1\n", out.String())
	assert.False(t, printer.Terminal())

	_, err = svc.Streams().Printer("toml")
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	assert.Same(t, &out, svc.Streams().IO().Out)
}

// fakeRepository reads files by name from a map.
type fakeRepository map[string]string

// errTampered is what fakeRepository refuses a file it does not have with.
var errTampered = errors.New("tampered")

// Read returns the contents of path.
func (r fakeRepository) Read(path string) (io.ReadCloser, error) {
	content, ok := r[path]
	if !ok {
		return nil, errTampered
	}

	return io.NopCloser(strings.NewReader(content)), nil
}

// ValidateAll validates nothing.
func (fakeRepository) ValidateAll() error { return nil }

// TestServicesIntegrity checks that files are opened through the integrity
// repository tt was configured with.
//
//nolint:paralleltest // Changes the CmdCtx.
func TestServicesIntegrity(t *testing.T) {
	restoreProcess(t)

	cmd.GetCmdCtxPtr().Integrity.Repository = fakeRepository{"good": "content"}

	svc := readyServices(strings.NewReader(""), io.Discard, io.Discard)

	file, err := svc.Integrity().Open("good")
	require.NoError(t, err)

	content, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))

	_, err = svc.Integrity().Open("bad")
	require.ErrorIs(t, err, errTampered)
}

// TestServicesExit checks that Exit hands the error to the process's exit.
func TestServicesExit(t *testing.T) {
	t.Parallel()

	svc := readyServices(strings.NewReader(""), io.Discard, io.Discard)

	var exited []error

	svc.exit = func(err error) { exited = append(exited, err) }

	failure := sdk.WithCode(sdk.ExitPartial, errors.New("half done"))

	svc.Exit(failure)
	svc.Exit(nil)

	assert.Equal(t, []error{failure, nil}, exited)
}

// TestServicesClusterConfigFile checks a cluster configuration file that
// exists, one that does not and a source that names nothing.
func TestServicesClusterConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(path, []byte(`
groups:
  g:
    replicasets:
      r:
        instances:
          i: {}
`), 0o600))

	svc := readyServices(strings.NewReader(""), io.Discard, io.Discard)

	cfg, err := svc.ClusterConfig(t.Context(), sdk.FileSource(path))
	require.NoError(t, err)
	assert.Equal(t, dir, cfg.Dir)

	names, err := sdk.Instances(cfg.Config)
	require.NoError(t, err)
	assert.Equal(t, []string{"i"}, names)

	_, err = svc.ClusterConfig(t.Context(), sdk.FileSource(filepath.Join(dir, "missing.yaml")))
	require.ErrorIs(t, err, sdk.ErrNotFound)
	require.ErrorContains(t, err, "missing.yaml")

	_, err = svc.ClusterConfig(t.Context(), sdk.ClusterSource{})
	require.ErrorIs(t, err, errNoSource)
	assert.NotErrorIs(t, err, sdk.ErrNotFound)
}
