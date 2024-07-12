package running

import (
	"bytes"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

func TestNewColorizedPrefixWriter(t *testing.T) {
	var buf bytes.Buffer
	wr := NewColorizedPrefixWriter(&buf, *color.New(color.FgGreen), "prefix ")
	wr.Write([]byte("hello\n"))
	wr.Write([]byte(" E> world\n"))

	var expected bytes.Buffer
	clr := color.New(color.FgGreen)

	clr.Fprint(&expected, "prefix ")
	expected.WriteString("hello\n")

	clr.Fprint(&expected, "prefix ")
	clr.Add(color.FgRed, color.Bold)
	clr.Fprint(&expected, " E> world\n")
	assert.Equal(t, expected.Bytes(), buf.Bytes())
}
