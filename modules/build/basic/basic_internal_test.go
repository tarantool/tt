package basic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
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

// TestExport pins the scripts of the exports of make and shell: the command
// Run runs, each argument a word of sh, and the tool it looks for.
func TestExport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		bld    build.Build
		script string
		tools  []string
	}{
		{
			"make", makeBuild{config: MakeConfig{MakeTarget: "all", Entrypoint: "", Flags: nil}},
			"make -C . -f Makefile all", []string{"make"},
		},
		{
			"make with flags", makeBuild{config: MakeConfig{
				MakeTarget: "build", Entrypoint: "tt.mk", Flags: []string{"-j4", "V=1", "N=it's"},
			}},
			`make -C . -f tt.mk build -j4 'V=1' 'N=it'\''s'`, []string{"make"},
		},
		{
			"shell", shellBuild{config: ShellConfig{
				Command: "sh", Args: []string{"-c", "echo $(date) > out\necho done", ""},
			}},
			"env -- sh -c 'echo $(date) > out\necho done' ''", []string{"sh"},
		},
		{
			"shell of a path", shellBuild{config: ShellConfig{
				Command: "./build.sh", Args: nil,
			}},
			"env -- ./build.sh", nil,
		},
		{
			// A word sh reads itself is the program of the name, as for Run.
			"shell of a reserved word", shellBuild{config: ShellConfig{
				Command: "if", Args: []string{"x"},
			}},
			"env -- if x", []string{"if"},
		},
		{
			"shell of a function of the script", shellBuild{config: ShellConfig{
				Command: "tt_output", Args: nil,
			}},
			"env -- tt_output", []string{"tt_output"},
		},
	} {
		exporter, ok := test.bld.(build.Exporter)
		require.True(t, ok, test.name)

		//nolint:exhaustruct // make and shell read nothing of the context.
		export, err := exporter.Export(&build.ExportContext{})
		require.NoError(t, err, test.name)
		assert.Equal(t, test.script, export.Script, test.name)
		assert.Equal(t, test.tools, export.Tools, test.name)
		assert.Empty(t, export.Lost, test.name)
	}
}

// TestExportOfACommandOfAnAssignment: a command of shell with a = is made
// with tt alone, as env, which the script runs the command by, would take
// it for a variable to set.
func TestExportOfACommandOfAnAssignment(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // shell reads nothing of the context.
	_, err := shellBuild{config: ShellConfig{Command: "A=1", Args: []string{"x"}}}.Export(
		&build.ExportContext{})
	require.ErrorIs(t, err, build.ErrNoExport)

	var fieldErr *build.FieldError

	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "command", fieldErr.Field)
	assert.Equal(t, `"A=1" has a =, and the script runs the command by env, as Run execs it, `+
		`which takes a word with a = for a variable to set`, fieldErr.Msg)
}

// TestOutputNames pins the programs a make or shell build names: the files
// output lists, by base name, each once, in their order.
func TestOutputNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"gw", "gw-cli"}, outputNames([]string{"out/gw", "out/gw-cli", "gw"}))
	assert.Empty(t, outputNames(nil))
}
