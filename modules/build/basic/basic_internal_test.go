package basic

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMakeArgs(t *testing.T) {
	t.Parallel()

	config := MakeConfig{MakeTarget: "all", Entrypoint: "", Flags: []string{"-j4", "V=1"}}

	got := makeArgs(config, "/proj")
	want := []string{"-C", "/proj", "-f", "Makefile", "all", "-j4", "V=1"}
	assert.Equal(t, want, got)
}

func TestMakeArgsCustomEntrypoint(t *testing.T) {
	t.Parallel()

	config := MakeConfig{MakeTarget: "build", Entrypoint: "Makefile.tt", Flags: nil}

	got := makeArgs(config, "/proj")
	assert.Equal(t, []string{"-C", "/proj", "-f", "Makefile.tt", "build"}, got)
}

// TestOutputNames pins the programs a make or shell build names: the files
// output lists, by base name, each once, in their order.
func TestOutputNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"gw", "gw-cli"}, outputNames([]string{"out/gw", "out/gw-cli", "gw"}))
	assert.Empty(t, outputNames(nil))
}
