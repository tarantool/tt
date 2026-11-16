package connect

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-prompt"
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

func TestCmdExecutorComplete(t *testing.T) {
	executor := newCmdExecutor()

	texts := func(text string) []string {
		t.Helper()

		buf := prompt.NewBuffer()
		buf.InsertText(text, false, true)

		doc := buf.Document()

		suggests, ok := executor.Complete(doc.TextBeforeCursor(),
			doc.GetWordBeforeCursorUntilSeparator(tarantoolWordSeparators))
		require.True(t, ok, text)

		result := make([]string, 0, len(suggests))
		for _, suggest := range suggests {
			result = append(result, suggest.Text)
		}

		return result
	}

	assert.Equal(t, []string{
		"set delimiter", "set graphics", "set language", "set output",
		"set table_column_width", "set table_format",
	}, texts(`\se`))
	assert.Equal(t, []string{"output"}, texts(`\set o`))
	assert.Equal(t, []string{"output"}, texts(`  \set o`))
	assert.Equal(t, []string{"lua", "table", "ttable", "yaml"}, texts(`\set output `))
	assert.Equal(t, []string{"table", "ttable"}, texts(`\set output t`))
	assert.Equal(t, []string{"ttable"}, texts(`\set output tt`))
	assert.Empty(t, texts(`\set output yaml`))
	assert.Equal(t, []string{"false", "true"}, texts(`\set graphics `))
	assert.Equal(t, []string{"lua", "sql"}, texts(`\set language `))
	assert.Equal(t, []string{"xG", "xT", "xg", "xl", "xt", "xw", "xy"}, texts(`\x`))
	assert.Subset(t, texts(`\`), []string{"help", "q", "quit", "set output", "shortcuts"})
	assert.Equal(t, []string{"quit"}, texts(`\q`))
	assert.Empty(t, texts(`\quit`))

	suggests, _ := executor.Complete(`\hi`, "hi")
	require.Len(t, suggests, 1)
	assert.Equal(t, prompt.Suggest{
		Text:        "history",
		Description: "show the last <count> executed commands (default 10)",
	}, suggests[0])

	for _, text := range []string{"", "se", "box.info", "x = '\\se"} {
		_, ok := executor.Complete(text, "")
		assert.False(t, ok, text)
	}
}

func TestHelpText(t *testing.T) {
	help, err := newHelpCmd(cmdInfos).Run(&Console{}, "\\help", nil)
	require.NoError(t, err)

	assert.Contains(t, help,
		"\n  tt connect runs the backslash commands itself and adds some of its own.\n")
	assert.NotContains(t, help, "is using.")
	assert.Contains(t, help, "\n  \\help, ?")
}
