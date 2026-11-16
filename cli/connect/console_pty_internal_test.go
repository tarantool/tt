package connect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/connect/internal/luabody"
	"github.com/tarantool/tt/v3/cli/connector"
)

// ptyHelperEnv makes the test binary run TestConsolePtyHelper, the console
// on the terminal the parent test gives it.
const ptyHelperEnv = "TT_CONNECT_PTY_HELPER"

// echoConn is a connector whose instance answers a command with the command
// itself.
type echoConn struct{}

func (echoConn) Eval(expr string, args []any, opts connector.RequestOpts) ([]any, error) {
	res, ok := opts.ResData.(*[]string)
	if !ok {
		return nil, errors.New("unexpected result type")
	}

	if expr == luabody.GetSuggestionsFuncBody() {
		*res = nil

		return nil, nil
	}

	cmd, _ := args[0].(string)

	*res = []string{"---\n- executed: " + strings.ReplaceAll(cmd, "\n", " ") + "\n...\n"}

	return nil, nil
}

func (echoConn) Close() error { return nil }

// TestConsolePtyHelper runs the console on its terminal, when the parent
// test runs it; it stops at Ctrl-D.
func TestConsolePtyHelper(t *testing.T) {
	if os.Getenv(ptyHelperEnv) == "" {
		t.Skip("runs as the console of TestConsoleExecutesEveryPastedLine")
	}

	console, err := newConsole(echoConn{}, ConnectCtx{}, "", "pty")
	require.NoError(t, err)

	defer console.Close()

	require.NoError(t, console.Run())
}

// ptyOutput collects what the console writes to its terminal.
type ptyOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *ptyOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.buf.Write(p)
}

func (o *ptyOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.buf.String()
}

// waitFor waits for the console to write each of wants.
func (o *ptyOutput) waitFor(t *testing.T, wants ...string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out := o.String()

		done := true

		for _, want := range wants {
			if !strings.Contains(out, want) {
				done = false
			}
		}

		if done {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("the console did not write %q; it wrote:\n%q", wants, o.String())
}

// startPtyConsole starts the console on a new terminal and returns the
// terminal and what the console writes to it.
func startPtyConsole(t *testing.T) (*os.File, *ptyOutput) {
	t.Helper()

	//nolint:gosec // The test binary runs itself.
	// Not t.Context(): it is done before the cleanup quits the console.
	cmd := exec.CommandContext(context.Background(), os.Args[0],
		"-test.run=^TestConsolePtyHelper$", "-test.count=1")

	cmd.Env = append(os.Environ(), ptyHelperEnv+"=1", "HOME="+t.TempDir())

	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	require.NoError(t, err)

	out := &ptyOutput{}
	copied := make(chan struct{})

	go func() {
		_, _ = io.Copy(out, tty)

		close(copied)
	}()

	t.Cleanup(func() {
		// Ctrl-D on an empty line quits the console.
		_, _ = tty.Write([]byte{0x4})

		waited := make(chan error, 1)

		go func() { waited <- cmd.Wait() }()

		select {
		case err := <-waited:
			if err != nil {
				t.Errorf("the console failed: %s; it wrote:\n%q", err, out.String())
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()

			t.Errorf("the console did not quit; it wrote:\n%q", out.String())
		}

		_ = tty.Close()

		<-copied
	})

	out.waitFor(t, "pty> ")

	return tty, out
}

// TestConsoleExecutesEveryPastedLine writes lines with their line breaks in
// one write, as a terminal pastes them, and checks that every line is
// executed, as if it were typed.
func TestConsoleExecutesEveryPastedLine(t *testing.T) {
	tty, out := startPtyConsole(t)

	_, err := tty.WriteString("return 1\nreturn 2\n")
	require.NoError(t, err)
	out.waitFor(t, "executed: return 1", "executed: return 2")

	// A pasted line break is a carriage return: the terminal pastes "\r".
	_, err = tty.WriteString("return 3\rreturn 4\r")
	require.NoError(t, err)
	out.waitFor(t, "executed: return 3", "executed: return 4")

	// An unfinished statement goes on in the next pasted line.
	_, err = tty.WriteString("function f()\r\treturn 5\rend\r")
	require.NoError(t, err)
	out.waitFor(t, "executed: function f()     return 5 end")
}

// TestConsoleCompletesBackslashCommands types the start of a backslash
// command and Tab, and checks that the console offers its commands.
func TestConsoleCompletesBackslashCommands(t *testing.T) {
	tty, out := startPtyConsole(t)

	_, err := tty.WriteString("\\se")
	require.NoError(t, err)
	out.waitFor(t, "set language", "set output")

	// Tab selects the first one, \set delimiter, which is drawn again.
	shown := strings.Count(out.String(), "set delimiter")

	_, err = tty.WriteString("\t")
	require.NoError(t, err)

	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(out.String(), "set delimiter") == shown && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	_, err = tty.WriteString(" ;\r")
	require.NoError(t, err)

	_, err = tty.WriteString("return 1;\r")
	require.NoError(t, err)
	out.waitFor(t, "executed: return 1\r")
}

// TestConsoleBracketedPaste checks that the console asks the terminal to
// mark pastes and executes every line of a marked paste.
func TestConsoleBracketedPaste(t *testing.T) {
	tty, out := startPtyConsole(t)

	out.waitFor(t, "\x1b[?2004h")

	_, err := tty.WriteString("\x1b[200~return 1\rreturn\t2\r\x1b[201~")
	require.NoError(t, err)
	out.waitFor(t, "executed: return 1", "executed: return    2")
}
