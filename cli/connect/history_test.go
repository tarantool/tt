package connect

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseHistoryCells(t *testing.T) {
	t.Run("actual", func(t *testing.T) {
		lines := []string{
			"#1685979611",
			"print(1)",
			"#1685979622",
			"print(2",
			")",
			"#1685979630",
			"#1685979645",
			"box.cfg{}",
		}
		commands, timestamps := parseHistoryCells(lines)
		assert.Equal(t, []string{"print(1)", "print(2\n)", "box.cfg{}"}, commands)
		assert.Equal(t, []int64{1685979611, 1685979622, 1685979645}, timestamps)
	})

	t.Run("# in command", func(t *testing.T) {
		lines := []string{
			"#1111111111",
			"a={1,2,",
			"3}",
			"#2222222222",
			"#3333333333",
			"for i=0,",
			"#a",
			",1 do",
			"print(i)",
			"end",
			"#4444444444",
			"os.exit()",
		}
		commands, timestamps := parseHistoryCells(lines)
		assert.Equal(t,
			[]string{"a={1,2,\n3}", "for i=0,\n#a\n,1 do\nprint(i)\nend", "os.exit()"},
			commands)
		assert.Equal(t, []int64{1111111111, 3333333333, 4444444444}, timestamps)
	})

	t.Run("legacy", func(t *testing.T) {
		lines := []string{
			"a=3",
			"b=4",
			"print(a+b)",
			"box.cfg{}",
		}
		commands, timestamps := parseHistoryCells(lines)
		assert.Equal(t, []string{"a=3", "b=4", "print(a+b)", "box.cfg{}"}, commands)
		assert.Equal(t, 4, len(timestamps))
	})
}

func TestHistoryAppend(t *testing.T) {
	limit := 20

	h, _ := newCommandHistory("", limit)
	for i := 0; i < limit; i++ {
		h.appendCommand(fmt.Sprintf("command%d", i))
		assert.Equal(t, len(h.commands), i+1)
		assert.Equal(t, len(h.timestamps), i+1)
	}

	for i := limit; i < 2*limit; i++ {
		h.appendCommand(fmt.Sprintf("command%d", i))
		assert.Equal(t, len(h.commands), limit)
		assert.Equal(t, len(h.timestamps), limit)
		assert.Equal(t, fmt.Sprintf("command%d", i+1-limit), h.commands[0])
	}
}
