package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as the child process: with helperEnv set, TestMain
// runs the named helper mode instead of the tests.
const (
	helperEnv = "SUPERVISOR_TEST_HELPER"
	dirEnv    = "SUPERVISOR_TEST_DIR"
	codeEnv   = "SUPERVISOR_TEST_CODE"
	stdinEnv  = "SUPERVISOR_TEST_STDIN"
	// The pid files of the supervise mode.
	pidFileEnv      = "SUPERVISOR_TEST_PID_FILE"
	childPidFileEnv = "SUPERVISOR_TEST_CHILD_PID_FILE"
)

// Helper modes.
const (
	// modeServe records forwarded signals and exits with a code telling
	// which stop signal it got: SIGINT 10, SIGTERM 11, SIGQUIT 12.
	modeServe = "serve"
	// modeStubborn ignores the stop signals and records the others.
	modeStubborn = "stubborn"
	// modeExit exits at once with the code from codeEnv.
	modeExit = "exit"
	// modeFamily starts a stubborn grandchild in its own process group, then
	// behaves as modeStubborn.
	modeFamily = "family"
	// modeSupervise runs an Engine over a serve child with real signals.
	modeSupervise = "supervise"
	// modeOrphan starts a stubborn grandchild that inherits its standard
	// output, writes its pid into the grandchild file and exits at once.
	modeOrphan = "orphan"
	// modePanicNil runs a check that panics with nil and exits 0 if the
	// engine took it for a failed check, 1 if not.
	modePanicNil = "panic-nil"
)

// Exit codes of modeServe per stop signal.
const (
	exitOnInt  = 10
	exitOnTerm = 11
	exitOnQuit = 12
)

func TestMain(m *testing.M) {
	mode := os.Getenv(helperEnv)
	if mode != "" {
		os.Exit(runHelper(mode))
	}

	os.Exit(m.Run())
}

// helperFile is a file a helper with pid writes into dir.
func helperFile(dir, name string, pid int) string {
	return filepath.Join(dir, name+"."+strconv.Itoa(pid))
}

func runHelper(mode string) int {
	dir := os.Getenv(dirEnv)

	switch mode {
	case modeServe:
		return serve(dir, false)
	case modeStubborn:
		return serve(dir, true)
	case modeExit:
		return exitAtOnce(dir)
	case modeFamily:
		return family(dir)
	case modeSupervise:
		return supervise(dir)
	case modeOrphan:
		orphan(dir)

		return 0
	case modePanicNil:
		return checkPanicNil()
	}

	fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)

	return 2
}

// mustWrite writes a file whole: a reader sees it either absent or complete,
// never created and still empty.
func mustWrite(path, content string) {
	temp := path + ".tmp"

	err := os.WriteFile(temp, []byte(content), 0o600)
	if err != nil {
		panic(err)
	}

	err = os.Rename(temp, path)
	if err != nil {
		panic(err)
	}
}

// serve writes its arguments, optionally its standard input, then the ready
// file, and records every signal it is sent until a stop signal ends it.
func serve(dir string, stubborn bool) int {
	pid := os.Getpid()
	sigs := make(chan os.Signal, 64)

	if stubborn {
		signal.Ignore(syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
		signal.Notify(sigs, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGHUP)
	} else {
		signal.Notify(sigs, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGHUP,
			syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	}

	mustWrite(helperFile(dir, "args", pid), strings.Join(os.Args[1:], " "))

	if os.Getenv(stdinEnv) != "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			panic(err)
		}

		mustWrite(helperFile(dir, "stdin", pid), string(data))
	}

	mustWrite(helperFile(dir, "ready", pid), "")

	for sig := range sigs {
		switch sig {
		case syscall.SIGINT:
			return exitOnInt
		case syscall.SIGTERM:
			return exitOnTerm
		case syscall.SIGQUIT:
			return exitOnQuit
		}

		appendLine(helperFile(dir, "signals", pid), sig.String())
	}

	return 0
}

func appendLine(path, line string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}

	_, err = fmt.Fprintln(file, line)
	if err != nil {
		panic(err)
	}

	err = file.Close()
	if err != nil {
		panic(err)
	}
}

func exitAtOnce(dir string) int {
	mustWrite(helperFile(dir, "args", os.Getpid()), strings.Join(os.Args[1:], " "))

	code, err := strconv.Atoi(os.Getenv(codeEnv))
	if err != nil {
		panic(err)
	}

	return code
}

// family starts a stubborn grandchild, which stays in the family's process
// group, and writes its pid into the grandchild file.
func family(dir string) int {
	startGrandchild(dir)

	return serve(dir, true)
}

// orphan leaves a stubborn grandchild holding its standard output behind.
func orphan(dir string) {
	startGrandchild(dir)
}

// checkPanicNil runs a check that panics with nil through the engine's
// wrapper of Options.Check.
func checkPanicNil() int {
	engine := &Engine{opts: Options{Check: func(context.Context) error {
		panic(nil)
	}}}

	err := engine.check(context.Background())

	_, _ = fmt.Fprintln(os.Stdout, err)

	if errors.Is(err, ErrCheckPanicked) {
		return 0
	}

	return 1
}

// startGrandchild starts a stubborn helper that shares the standard output,
// and writes its pid into the grandchild file.
func startGrandchild(dir string) {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}

	grandchild := exec.CommandContext(context.Background(), exe)

	grandchild.Env = append(os.Environ(), helperEnv+"="+modeStubborn)
	grandchild.Stdout = os.Stdout

	err = grandchild.Start()
	if err != nil {
		panic(err)
	}

	mustWrite(filepath.Join(dir, "grandchild"), strconv.Itoa(grandchild.Process.Pid))
}

// superviseSource starts one serve child and never restarts it.
type superviseSource struct{}

func (superviseSource) Next(context.Context) (Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return Spec{}, err
	}

	return Spec{
		Path: exe,
		Env: append(os.Environ(), helperEnv+"="+modeServe,
			"GORACE=atexit_sleep_ms=0"),
		StopSignal:        syscall.SIGTERM,
		ForwardStopSignal: true,
		StopTimeout:       10 * time.Second,
	}, nil
}

func (superviseSource) Restart(context.Context, Exit) (bool, error) {
	return false, nil
}

// supervise runs an Engine that takes real signals. It writes the exit code of
// its child into the exit file and a line per reload into the reloaded file.
func supervise(dir string) int {
	engine, err := New(superviseSource{}, Options{
		PidFile:      os.Getenv(pidFileEnv),
		ChildPidFile: os.Getenv(childPidFileEnv),
		StopSignals:  []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT},
		ReloadSignal: syscall.SIGHUP,
		OnReload: func() error {
			appendLine(filepath.Join(dir, "reloaded"), "reload")

			return nil
		},
		OnEvent: func(event Event) {
			exit, ok := event.(Exit)
			if ok {
				mustWrite(filepath.Join(dir, "exit"), strconv.Itoa(exit.State.ExitCode()))
			}
		},
	})
	if err != nil {
		panic(err)
	}

	err = engine.Run(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return 0
}
