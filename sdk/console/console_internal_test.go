package console

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-prompt"
)

// closedHandler answers every statement with nil, the way a handler reports
// that its connection is closed.
type closedHandler struct {
	closed int
}

func (*closedHandler) Title() string { return "test" }

func (*closedHandler) Validate(string) bool { return true }

func (*closedHandler) Complete(prompt.Document) []prompt.Suggest { return nil }

func (*closedHandler) Execute(string) any { return nil }

func (h *closedHandler) Close() { h.closed++ }

func TestNewConsole_RequiresExit(t *testing.T) {
	stopped, err := NewConsole(ConsoleOpts{
		Handler: &closedHandler{},
		History: nil,
		Format:  FormatAsTable(),
		Exit:    nil,
	})
	require.ErrorIs(t, err, errNoExitHasBeenSet)
	require.ErrorIs(t, stopped.Run(), errConsoleStopped)
}

func TestNewConsole_RequiresHandler(t *testing.T) {
	stopped, err := NewConsole(ConsoleOpts{
		Handler: nil,
		History: nil,
		Format:  FormatAsTable(),
		Exit:    func(error) {},
	})
	require.ErrorIs(t, err, errNoHandlerForCommandsHasBeenSet)
	require.ErrorIs(t, stopped.Run(), errConsoleStopped)
}

func TestConsole_ExitOnClosedConnection(t *testing.T) {
	handler := &closedHandler{}

	var exits []error

	console, err := NewConsole(ConsoleOpts{
		Handler: handler,
		History: nil,
		Format:  FormatAsTable(),
		Exit: func(err error) {
			// The handler is closed before the console asks to exit.
			assert.Equal(t, 1, handler.closed)

			exits = append(exits, err)
		},
	})
	require.NoError(t, err)

	console.execute("return 1")

	require.Len(t, exits, 1)
	require.NoError(t, exits[0])
	assert.Equal(t, 1, handler.closed)
}
