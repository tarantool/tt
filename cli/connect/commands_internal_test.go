package connect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/connector"
)

func TestConsoleHistoryOutput(t *testing.T) {
	t.Run("history unavailable", func(t *testing.T) {
		console := Console{}

		actual, err := getHistoryFunc(&console, "", nil)
		require.NoError(t, err)
		assert.Empty(t, actual)
	})

	t.Run("empty command list", func(t *testing.T) {
		history, err := newCommandHistory("test", 100)
		require.NoError(t, err)

		console := Console{history: history}

		actual, err := getHistoryFunc(&console, "", nil)
		require.NoError(t, err)
		assert.Empty(t, actual)
	})
	t.Run("history is less than max len", func(t *testing.T) {
		history, err := newCommandHistory("test", 100)
		require.NoError(t, err)

		history.appendCommand("test1")
		history.appendCommand("test2")

		console := Console{
			history:  history,
			connOpts: connector.ConnectOpts{MaxOutputHistoryLen: 5},
		}

		actual, err := getHistoryFunc(&console, "", nil)
		require.NoError(t, err)
		assert.Equal(t, "test1\n-----\ntest2", actual)
	})
	t.Run("history is equal max len", func(t *testing.T) {
		history, err := newCommandHistory("test", 100)
		require.NoError(t, err)

		history.appendCommand("test1")
		history.appendCommand("test2")
		history.appendCommand("test3")

		console := Console{
			history:  history,
			connOpts: connector.ConnectOpts{MaxOutputHistoryLen: 3},
		}

		actual, err := getHistoryFunc(&console, "", nil)
		require.NoError(t, err)
		assert.Equal(t, "test1\n-----\ntest2\n-----\ntest3", actual)
	})
	t.Run("history is more max len", func(t *testing.T) {
		history, err := newCommandHistory("test", 100)
		require.NoError(t, err)

		history.appendCommand("test1")
		history.appendCommand("test2")
		history.appendCommand("test3")
		history.appendCommand("test4")

		console := Console{
			history:  history,
			connOpts: connector.ConnectOpts{MaxOutputHistoryLen: 3},
		}

		actual, err := getHistoryFunc(&console, "", nil)
		require.NoError(t, err)
		assert.Equal(t, "test2\n-----\ntest3\n-----\ntest4", actual)
	})
}
