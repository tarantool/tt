package sdktest_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/sdk/sdktest"
)

// fatalTB is a testing.TB whose Fatalf records the message and ends the
// goroutine, so a test can check that the fake fails the test it is given.
type fatalTB struct {
	testing.TB

	message string
}

// Fatalf records the message and stops the calling goroutine.
func (f *fatalTB) Fatalf(format string, args ...any) {
	f.message = fmt.Sprintf(format, args...)

	runtime.Goexit()
}

// fatal runs body with a fatalTB wrapping t and returns the message body
// failed with, or "" when it returned normally.
func fatal(t *testing.T, body func(tb testing.TB)) string {
	t.Helper()

	tb := &fatalTB{TB: t, message: ""}

	var wg sync.WaitGroup

	wg.Go(func() { body(tb) })
	wg.Wait()

	return tb.message
}

func TestServicesPanicBeforeStart(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithName("mymod"))

	calls := map[string]func(){
		"Tarantool": func() { _, _ = services.Tarantool() },
		"Integrity": func() { _ = services.Integrity() },
		"Project":   func() { _ = services.Project() },
		"Confirm":   func() { _, _ = services.Confirm("sure?", true) },
		"Streams":   func() { _ = services.Streams() },
		"ClusterConfig": func() {
			_, _ = services.ClusterConfig(t.Context(), sdk.AppSource("app"))
		},
		"Exit": func() { services.Exit(nil) },
	}

	for method, call := range calls {
		assert.PanicsWithValue(t, `module "mymod" used Services.`+method+
			" before tt was configured; use it in a command's hooks, not in the constructor",
			call, method)
	}

	assert.NotPanics(t, func() { services.Log().Info("constructing") })

	services.Start()

	for method, call := range calls {
		if method == "Confirm" || method == "Exit" {
			continue // Confirm needs an answer, Exit ends the command; covered below.
		}

		assert.NotPanics(t, call, method)
	}
}

func TestServicesConfirm(t *testing.T) {
	t.Parallel()

	t.Run("answers are consumed in order", func(t *testing.T) {
		t.Parallel()

		services := sdktest.New(t, sdktest.WithAnswers(true, false, true))
		services.Start()

		for i, want := range []bool{true, false, true} {
			got, err := services.Confirm(fmt.Sprintf("question %d?", i), !want)
			require.NoError(t, err)
			assert.Equal(t, want, got, "answer %d", i)
		}

		assert.Equal(t, "question 0? [y/n]: question 1? [y/n]: question 2? [y/n]: ",
			services.Stderr())
	})

	t.Run("running out of answers fails the test", func(t *testing.T) {
		t.Parallel()

		message := fatal(t, func(tb testing.TB) {
			tb.Helper()

			services := sdktest.New(tb, sdktest.WithAnswers(true))
			services.Start()

			_, _ = services.Confirm("first?", false)
			_, _ = services.Confirm("second?", false)
		})

		assert.Contains(t, message, `Confirm("second?"): no answer left`)
	})

	t.Run("no answers given fails the test", func(t *testing.T) {
		t.Parallel()

		message := fatal(t, func(tb testing.TB) {
			tb.Helper()

			services := sdktest.New(tb)
			services.Start()

			_, _ = services.Confirm("sure?", false)
		})

		assert.Contains(t, message, "no answer left")
	})

	t.Run("no prompt returns the fallback", func(t *testing.T) {
		t.Parallel()

		services := sdktest.New(t, sdktest.WithNoPrompt())
		services.Start()

		for _, fallback := range []bool{true, false} {
			got, err := services.Confirm("sure?", fallback)
			require.NoError(t, err)
			assert.Equal(t, fallback, got)
		}

		assert.Empty(t, services.Stderr(), "nothing is asked")
	})

	t.Run("answers and no prompt exclude each other", func(t *testing.T) {
		t.Parallel()

		message := fatal(t, func(tb testing.TB) {
			tb.Helper()
			sdktest.New(tb, sdktest.WithAnswers(true), sdktest.WithNoPrompt())
		})

		assert.Contains(t, message, "exclude each other")
	})
}

func TestServicesTarantool(t *testing.T) {
	t.Parallel()

	none := sdktest.New(t)
	none.Start()

	_, err := none.Tarantool()
	require.ErrorIs(t, err, sdk.ErrNotFound)

	version := sdk.TarantoolVersion{Major: 3, Minor: 2, Edition: sdk.EditionEE}
	services := sdktest.New(t, sdktest.WithTarantool("/opt/tarantool/bin/tarantool", version))
	services.Start()

	tarantool, err := services.Tarantool()
	require.NoError(t, err)
	assert.Equal(t, "/opt/tarantool/bin/tarantool", tarantool.Path())

	got, err := tarantool.Version()
	require.NoError(t, err)
	assert.Equal(t, version, got)
}

func TestServicesIntegrity(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))

	services := sdktest.New(t)
	services.Start()

	file, err := services.Integrity().Open(path)
	require.NoError(t, err)

	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, "content", string(data))

	errTampered := errors.New("tampered")
	refusing := sdktest.New(t, sdktest.WithIntegrity(func(string) (io.ReadCloser, error) {
		return nil, errTampered
	}))
	refusing.Start()

	_, err = refusing.Integrity().Open(path)
	require.ErrorIs(t, err, errTampered)
}

func TestServicesProject(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	dir, err := services.Project().Dir()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir), dir)
	assert.DirExists(t, dir)

	relative := sdktest.New(t, sdktest.WithProject("some/project"))
	relative.Start()

	dir, err = relative.Project().Dir()
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, "some", "project"), dir)
}

func TestServicesStreams(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithStdin(strings.NewReader("input")))
	services.Start()

	streams := services.Streams()

	input, err := io.ReadAll(streams.IO().In)
	require.NoError(t, err)
	assert.Equal(t, "input", string(input))

	_, err = io.WriteString(streams.IO().Err, "note\n")
	require.NoError(t, err)

	printer, err := streams.Printer(output.FormatJSON)
	require.NoError(t, err)
	require.NoError(t, printer.Emit(result{Name: "x"}))

	assert.JSONEq(t, `{"name": "x"}`, services.Stdout())
	assert.Equal(t, "note\n", services.Stderr())

	_, err = streams.Printer(output.FormatYAML)
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	humanServices := sdktest.New(t)
	humanServices.Start()

	human, err := humanServices.Streams().Printer(output.FormatHuman)
	require.NoError(t, err)
	require.NoError(t, human.Emit(result{Name: "y"}))
	assert.Equal(t, "name y\n", humanServices.Stdout())
}

func TestServicesLog(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithName("mymod"))
	services.Log().Info("hello", "key", "value")

	records := services.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "hello", records[0].Message)
	assert.Equal(t, "mymod", records[0].Map()["module"])
	assert.Equal(t, "value", records[0].Map()["key"])

	defaultName := sdktest.New(t)
	defaultName.Log().Info("hello")
	assert.Equal(t, "test", defaultName.Records()[0].Map()["module"])
}

// result is a command result for the printer tests.
type result struct {
	Name string `json:"name"`
}

// Human writes the result for people.
func (r result) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "name %s\n", r.Name)

	return err
}
