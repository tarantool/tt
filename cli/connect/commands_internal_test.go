package connect

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyConsole makes a console whose history holds count commands,
// "test1" to "test<count>".
func historyConsole(t *testing.T, count int) *Console {
	t.Helper()

	history, err := newCommandHistory("test", 100)
	require.NoError(t, err)

	for i := 1; i <= count; i++ {
		history.appendCommand("test" + strconv.Itoa(i))
	}

	return &Console{history: history}
}

func TestConsoleHistoryOutput(t *testing.T) {
	t.Run("history unavailable", func(t *testing.T) {
		actual, err := getHistoryFunc(&Console{}, "", nil)
		require.NoError(t, err)
		assert.Empty(t, actual)
	})

	t.Run("empty command list", func(t *testing.T) {
		actual, err := getHistoryFunc(historyConsole(t, 0), "", nil)
		require.NoError(t, err)
		assert.Empty(t, actual)
	})

	t.Run("history is less than count", func(t *testing.T) {
		actual, err := getHistoryFunc(historyConsole(t, 2), "", []string{"5"})
		require.NoError(t, err)
		assert.Equal(t, "test1\n-----\ntest2", actual)
	})

	t.Run("history is equal to count", func(t *testing.T) {
		actual, err := getHistoryFunc(historyConsole(t, 3), "", []string{"3"})
		require.NoError(t, err)
		assert.Equal(t, "test1\n-----\ntest2\n-----\ntest3", actual)
	})

	t.Run("history is more than count", func(t *testing.T) {
		actual, err := getHistoryFunc(historyConsole(t, 4), "", []string{"3"})
		require.NoError(t, err)
		assert.Equal(t, "test2\n-----\ntest3\n-----\ntest4", actual)
	})

	t.Run("zero count", func(t *testing.T) {
		actual, err := getHistoryFunc(historyConsole(t, 2), "", []string{"0"})
		require.NoError(t, err)
		assert.Empty(t, actual)
	})

	t.Run("default count", func(t *testing.T) {
		actual, err := getHistoryFunc(historyConsole(t, DefaultOutputHistorySize+2), "", nil)
		require.NoError(t, err)
		assert.Equal(t, DefaultOutputHistorySize, strings.Count(actual, "\n-----\n")+1)
		assert.True(t, strings.HasPrefix(actual, "test3\n"), actual)
	})

	t.Run("invalid count", func(t *testing.T) {
		for _, arg := range []string{"-1", "ten", ""} {
			_, err := getHistoryFunc(historyConsole(t, 2), "", []string{arg})
			require.ErrorIs(t, err, errNotUnsigned, arg)
		}
	})

	t.Run("too many arguments", func(t *testing.T) {
		_, err := getHistoryFunc(historyConsole(t, 2), "", []string{"1", "2"})
		require.ErrorIs(t, err, errTheCommandExpectsZeroOrSingleArgument)
	})
}
